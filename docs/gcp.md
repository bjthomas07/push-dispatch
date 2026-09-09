# GCP deployment

These commands deploy into **your** project. The repository does not contain cloud
credentials and development tests use an emulator. A Firebase-enabled GCP project,
Firestore Native database, Firebase Auth, Android/iOS Firebase app registrations,
and billing for Cloud Run/Cloud Scheduler are prerequisites. Configure the APNs key
in Firebase before testing iOS delivery.

```sh
export PUSH_PROJECT=your-project
export PUSH_REGION=us-central1
export PUSH_APP=your-app
export PUSH_IMAGE="$PUSH_REGION-docker.pkg.dev/$PUSH_PROJECT/push-dispatch/server:initial"

gcloud services enable run.googleapis.com cloudbuild.googleapis.com \
  artifactregistry.googleapis.com cloudscheduler.googleapis.com \
  firestore.googleapis.com fcm.googleapis.com identitytoolkit.googleapis.com \
  --project "$PUSH_PROJECT"

gcloud artifacts repositories create push-dispatch --repository-format=docker \
  --location "$PUSH_REGION" --project "$PUSH_PROJECT"
gcloud builds submit --tag "$PUSH_IMAGE" --project "$PUSH_PROJECT" .
```

Create three dedicated service accounts:

```sh
gcloud iam service-accounts create push-api --project "$PUSH_PROJECT"
gcloud iam service-accounts create push-worker --project "$PUSH_PROJECT"
gcloud iam service-accounts create push-clock --project "$PUSH_PROJECT"

for account in push-api push-worker; do
  gcloud projects add-iam-policy-binding "$PUSH_PROJECT" \
    --member="serviceAccount:$account@$PUSH_PROJECT.iam.gserviceaccount.com" \
    --role=roles/datastore.user
done
gcloud projects add-iam-policy-binding "$PUSH_PROJECT" \
  --member="serviceAccount:push-api@$PUSH_PROJECT.iam.gserviceaccount.com" \
  --role=roles/firebaseauth.viewer
gcloud projects add-iam-policy-binding "$PUSH_PROJECT" \
  --member="serviceAccount:push-worker@$PUSH_PROJECT.iam.gserviceaccount.com" \
  --role=roles/firebasecloudmessaging.admin
```

Deploy the Firestore indexes and rules **only to a dedicated notification project**.
For an existing project, merge the index definitions and namespace-deny rule into
your existing files; do not overwrite application rules. The Admin SDK uses IAM
and bypasses these client rules.

```sh
firebase deploy --only firestore --project "$PUSH_PROJECT"
```

The device API is publicly reachable at the HTTP layer and verifies a revocation-
checked Firebase Auth token on every device request. Add application quotas and
rate limiting at your gateway. Self-service schedule routes are disabled by default;
add `--client-schedules` to the server arguments only after enforcing your per-user
schedule policy. Trusted server code can always use the library/CLI to schedule.

```sh
gcloud run deploy push-api --image "$PUSH_IMAGE" --region "$PUSH_REGION" \
  --project "$PUSH_PROJECT" --service-account="push-api@$PUSH_PROJECT.iam.gserviceaccount.com" \
  --args=serve --set-env-vars="GOOGLE_CLOUD_PROJECT=$PUSH_PROJECT,PUSH_APP=$PUSH_APP" \
  --allow-unauthenticated --max-instances=5

gcloud run jobs deploy push-worker --image "$PUSH_IMAGE" --region "$PUSH_REGION" \
  --project "$PUSH_PROJECT" --service-account="push-worker@$PUSH_PROJECT.iam.gserviceaccount.com" \
  --args=tick,--limit,100 --tasks=1 --parallelism=1 --cpu=1 --memory=512Mi \
  --max-retries=0 --task-timeout=600s \
  --set-env-vars="GOOGLE_CLOUD_PROJECT=$PUSH_PROJECT,PUSH_APP=$PUSH_APP"

gcloud run jobs add-iam-policy-binding push-worker --region "$PUSH_REGION" \
  --project "$PUSH_PROJECT" --member="serviceAccount:push-clock@$PUSH_PROJECT.iam.gserviceaccount.com" \
  --role=roles/run.invoker

gcloud scheduler jobs create http push-tick --location "$PUSH_REGION" \
  --project "$PUSH_PROJECT" --schedule='* * * * *' --time-zone=Etc/UTC \
  --uri="https://run.googleapis.com/v2/projects/$PUSH_PROJECT/locations/$PUSH_REGION/jobs/push-worker:run" \
  --http-method=POST --headers=Content-Type=application/json --message-body='{}' \
  --oauth-service-account-email="push-clock@$PUSH_PROJECT.iam.gserviceaccount.com" \
  --oauth-token-scope=https://www.googleapis.com/auth/cloud-platform
```

The Google API endpoint requires OAuth, rather than an OIDC audience token.
[Scheduler authentication](https://cloud.google.com/scheduler/docs/http-target-auth),
[scheduled Cloud Run jobs](https://cloud.google.com/run/docs/execute/jobs-on-schedule).

Start with one task and measure backlog and provider quota pressure. Increasing
`--tasks`/`--parallelism` up to 32 spreads the shard queries across processes.
Overlapping executions share transactional claims. A terminated execution's
unfinished claim becomes eligible after its lease expires. Deploy immutable image
digests for subsequent releases and verify indexes are ready before enabling ticks.

## Cost and capacity before enabling the cron job

Cloud Run Jobs bill at least one minute **per task**, including an empty tick.
With the resource settings above, one task each minute for 30 days has a worker
floor of about **$49.25 before free allowances** at the September 8, 2026
`us-central1` rates. The calculation is `43,200 × 60 × (0.000018 + 0.5 × 0.000002)`.
Thirty-two tasks each minute raise that floor to about **$1,575.94**, before other
services. Longer executions and retries add runtime. These are worker billing
floors, not an estimate of total operating cost or measured capacity.
[Cloud Run pricing](https://cloud.google.com/run/pricing).

Estimate Firestore usage from the actual operations:

| Normal operation, without retries/pruning | Document reads | Document writes |
|---|---:|---:|
| Register one device | 2 | 2 |
| Heartbeat for an existing device | 1 | 1 |
| Create/replace a schedule through the CLI | 0 | 1 |
| Process one successful daily occurrence | 4 | 2 |

The occurrence includes its query, claim transaction, user lookup, and finish
transaction. Finishing advances the existing schedule to the next local day.
Empty queries cost at least one read; 32 end-of-shard polls per minute contribute
up to roughly 1.38 million reads per 30 days. Transaction retries, ownership changes,
invalid-target cleanup, and optional deletion fences add operations. Use the rates
for your actual database location and available free quota.
[Firestore billing](https://cloud.google.com/firestore/pricing).

`--limit 100` applies per shard, so an invocation is capped at 3,200 claimed jobs
across all 32 shards. Processing is sequential inside each task. Measure oldest
due time, elapsed time per job, and provider quota errors before increasing task
count or the limit (maximum 1,000 per shard). The 500-target dispatcher batch limit
does not combine 500 different users' scheduled jobs into one batch.

FCM is currently listed as no-cost; Firestore reads/writes, Cloud Run, Scheduler,
Artifact Registry, build minutes, logs, and network usage have separate costs.
There is no promise that operating this stack is free at any scale.
[Firebase pricing](https://firebase.google.com/pricing).

## Verify delivery

After deployment, validate token/FID registration, foreground/background display,
taps, permission changes, logout/account switches, and a scheduled occurrence on
physical Android and iOS devices. Simulator/emulator tests cannot verify production
FCM/APNs credentials or delivery. No production deployment was performed as part
of the initial extraction.

Use the [quickstart](quickstart.md) for a first device and the
[troubleshooting guide](troubleshooting.md) to diagnose registration, scheduling,
and presentation separately.
