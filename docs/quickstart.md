# Send your first reminder

This walkthrough registers one device, sends a real notification, and creates and
cancels schedules. Use a Firebase **test project** and a test account/device. The
API runs on your computer; Firestore and FCM use your project. Cloud usage is billed
by the provider. No hosted push-dispatch account or published package is required.

## 1. Prepare the app and tools

You need Git, [mise](https://mise.jdx.dev/getting-started.html), the Google Cloud CLI,
Firebase CLI, curl 7.76+ and jq. Run the shell blocks in **Bash** on macOS or Linux.

In the Firebase console, create or select your test project, enable Firebase Auth
with your app's sign-in provider, and create a Firestore Native `(default)` database.
Register the Android/iOS app with that same project. Follow the
[Android](../android/README.md) or [Apple](../apple/README.md) integration guide.

Before sending, sign in on the device and grant notification permission. Android
must create a notification channel named `reminders`. For iOS, configure APNs in
Firebase and enable the app's push capability. Use a physical device for delivery
validation. Background the app for the first display check; foreground presentation
requires the callbacks described in the platform guides.

```sh
git clone https://github.com/bjthomas07/push-dispatch.git
cd push-dispatch
mise trust
mise install
mise run build
./bin/push-dispatch --help
```

In this terminal, set your project and app namespace:

```sh
export GOOGLE_CLOUD_PROJECT=your-firebase-test-project
export PUSH_APP=quickstart
export PUSH_OPERATOR=you@example.com
export PUSH_SERVICE_ACCOUNT="push-quickstart@$GOOGLE_CLOUD_PROJECT.iam.gserviceaccount.com"
```

`PUSH_APP` is a namespace inside Firestore, not your Firebase project ID or mobile
bundle ID. Use the same value for the API, example, and worker.

## 2. Configure server credentials

Use your authorized operator account explicitly. These commands create a test
service account with database, Auth lookup, and FCM permissions. They do not change
the default gcloud project. For production, use the separate API/worker identities
in the [GCP deployment guide](gcp.md).

```sh
gcloud services enable firestore.googleapis.com fcm.googleapis.com \
  identitytoolkit.googleapis.com iamcredentials.googleapis.com \
  --project "$GOOGLE_CLOUD_PROJECT" --account "$PUSH_OPERATOR"

gcloud iam service-accounts create push-quickstart \
  --project "$GOOGLE_CLOUD_PROJECT" --account "$PUSH_OPERATOR"

for role in roles/datastore.user roles/firebaseauth.viewer roles/firebasecloudmessaging.admin; do
  gcloud projects add-iam-policy-binding "$GOOGLE_CLOUD_PROJECT" \
    --member="serviceAccount:$PUSH_SERVICE_ACCOUNT" --role="$role" \
    --account "$PUSH_OPERATOR"
done

gcloud iam service-accounts add-iam-policy-binding "$PUSH_SERVICE_ACCOUNT" \
  --member="user:$PUSH_OPERATOR" --role=roles/iam.serviceAccountTokenCreator \
  --project "$GOOGLE_CLOUD_PROJECT" --account "$PUSH_OPERATOR"

gcloud auth application-default login \
  --impersonate-service-account="$PUSH_SERVICE_ACCOUNT" \
  --project "$GOOGLE_CLOUD_PROJECT" --account "$PUSH_OPERATOR"
```

The last command updates local Application Default Credentials (ADC). It requires
an interactive login and an operator allowed to impersonate that service account.
An existing `GOOGLE_APPLICATION_CREDENTIALS` value takes precedence over local ADC;
use the intended credentials for this test. No service-account key is needed.
Firebase ID tokens used by the device are separate from these server credentials.
[ADC impersonation](https://cloud.google.com/docs/authentication/set-up-adc-local-dev-environment#service-account-impersonation),
[Firebase Admin setup](https://firebase.google.com/docs/admin/setup).

Deploy the supplied rules and indexes to the **dedicated test project**:

```sh
firebase deploy --only firestore --project "$GOOGLE_CLOUD_PROJECT"
```

Wait for index creation to finish. For a project already serving an app, merge and
review its rules/indexes instead of replacing them with the example files. A deny
rule cannot override another matching allow rule.

## 3. Start the API

In this first terminal, leave the server running:

```sh
./bin/push-dispatch serve --listen 127.0.0.1:8080
```

In a **second Bash terminal**, enter the same checkout and set the same project
and namespace. The local server uses loopback HTTP; a deployed API must use HTTPS.

```sh
export GOOGLE_CLOUD_PROJECT=your-firebase-test-project
export PUSH_APP=quickstart
export PUSH_BASE_URL=http://127.0.0.1:8080
export PUSH_PLATFORM=android
export PUSH_TIMEZONE=America/New_York

curl --silent --show-error --fail "$PUSH_BASE_URL/healthz" --output /dev/null
```

An exit status of zero with no body is expected: `/healthz` returns HTTP 204. It
checks that the process is running, not its Firestore/FCM permissions.

## 4. Register your device

Obtain a fresh **Firebase Auth ID token**, the same signed-in user's UID, and the
device's **FCM registration token** from your test app. These are three different
values. A gcloud access token, Firebase custom token, or raw APNs token will not
work in their place. [Getting a Firebase ID token](https://firebase.google.com/docs/auth/admin/verify-id-tokens#retrieve_id_tokens_on_clients).

For iOS using Messaging FIDs, change `targetType` below to `fid` and set
`PUSH_PLATFORM=ios`; supply the Messaging-registered FID described in the Apple
guide. The following example uses an FCM registration token.

Enter values interactively so they are not literal commands in shell history:

```sh
read -r -p 'Firebase Auth UID: ' PUSH_USER_ID
read -r -s -p 'Firebase Auth ID token: ' PUSH_ID_TOKEN
printf '\n'
read -r -s -p 'FCM registration token: ' PUSH_DEVICE_TOKEN
printf '\n'
export PUSH_USER_ID PUSH_DEVICE_TOKEN

umask 077
PUSH_QUICKSTART_DIR=$(mktemp -d)
trap 'rm -rf -- "$PUSH_QUICKSTART_DIR"; unset PUSH_ID_TOKEN PUSH_DEVICE_TOKEN' EXIT

jq -n '{provider:"fcm", targetType:"token", target:env.PUSH_DEVICE_TOKEN,
  platform:env.PUSH_PLATFORM, permission:"granted", timezone:env.PUSH_TIMEZONE,
  enabled:true}' > "$PUSH_QUICKSTART_DIR/device.json"

push_api() {
  printf 'Authorization: Bearer %s\n' "$PUSH_ID_TOKEN" |
    curl --silent --show-error --fail-with-body --max-time 30 \
      --header @- --header 'Content-Type: application/json' "$@"
}

push_api --request POST --data-binary @"$PUSH_QUICKSTART_DIR/device.json" \
  "$PUSH_BASE_URL/v1/devices" > "$PUSH_QUICKSTART_DIR/receipt.json"

PUSH_INSTALLATION_ID=$(jq -er '.installationId' "$PUSH_QUICKSTART_DIR/receipt.json")
printf 'Registered installation: %s\n' "$PUSH_INSTALLATION_ID"
```

The response contains a 64-character `installationId`. Registration writes to
`apps/quickstart/users/{verifiedUid}` and the corresponding ownership record. The
API derives ownership from the ID token; it does not accept `PUSH_USER_ID` from the
registration body. For subsequent administrative commands, ensure the UID you
entered is that same test user.

The temporary directory is private and removed when you exit this shell. Do not
enable shell tracing or include tokens in bug reports.

## 5. Send a notification through the Go library

```sh
mise run example-send
```

This runs [a complete Go program](../examples/send/main.go): it reads the user's
active installations, sends through the FCM adapter and dispatcher, handles partial
failure, and retires only targets explicitly reported as unregistered. It sends a
real message titled **Hello from push-dispatch**.

For one eligible device, expect `accepted=1 failed=0`. Acceptance means FCM accepted
the request; verify the notification actually appears on the device. No eligible
installations or a partial failure produces a nonzero exit. Re-running this direct
send is a new notification and may duplicate a message already received.

## 6. Exercise the scheduler now

Create a one-off occurrence due immediately, then run the worker:

```sh
jq -n --arg uid "$PUSH_USER_ID" '{id:"quickstart-once", recipientId:$uid,
  at:(now | todateiso8601), message:{title:"Scheduled hello",
  body:"The scheduler processed your reminder.", apple:{sound:"default"},
  android:{channelId:"reminders", sound:"default"}}}' |
  ./bin/push-dispatch schedule

./bin/push-dispatch tick
```

`schedule` prints the ID, UTC `dueAt`, and shard. One shard's tick report should
show `Claimed: 1` and `Completed: 1`; empty shards report zero. A completed job can
also have no eligible devices, so always check device display. A second tick will
not send that completed one-off job again. Running `schedule` again replaces it.

## 7. Create a daily reminder and cancel it

```sh
jq --arg uid "$PUSH_USER_ID" --arg zone "$PUSH_TIMEZONE" \
  '.id = "quickstart-daily" | .recipientId = $uid | .daily.timezone = $zone' \
  examples/daily.json | ./bin/push-dispatch schedule
```

This schedules 08:30 in `PUSH_TIMEZONE`. Edit `daily.localTime` to another HHMM integer
as needed; `830` means 08:30, not minutes since midnight. If today's time has passed,
`dueAt` is tomorrow's occurrence. The worker calculates future dates using the IANA
timezone, including DST. To follow a user's travel, explicitly update the schedule's
timezone too; a device heartbeat does not do that.

`serve` does not run the scheduler. Run `tick` when the job is due, or configure a
recurring worker using the [GCP deployment guide](gcp.md). Its Cloud Run Job has a
minimum charge per task/run; review that guide before enabling continuous polling.
Create a daily schedule once, then update it only when preferences change.

Cancel both test schedules and disable the installation while its user can still
authenticate:

```sh
./bin/push-dispatch cancel --user "$PUSH_USER_ID" --id quickstart-daily
./bin/push-dispatch cancel --user "$PUSH_USER_ID" --id quickstart-once
push_api --request DELETE "$PUSH_BASE_URL/v1/devices/$PUSH_INSTALLATION_ID"
unset PUSH_DEVICE_TOKEN
```

Cancellation succeeds without output; device deletion returns HTTP 204. These
operations disable future work, retain state, and cannot recall a push already
accepted by FCM. Stop the first terminal's server with Ctrl-C. If a step fails,
use [troubleshooting](troubleshooting.md) before retrying a send.
