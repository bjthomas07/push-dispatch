# Troubleshooting

Start with the failing stage: device registration, schedule creation, worker
processing, provider acceptance, or device presentation. Use the same project,
`PUSH_APP`, and test Firebase Auth UID throughout the [quickstart](quickstart.md).

## Registration and authentication

| Symptom | Check and action |
| --- | --- |
| API connection refused | Start `serve` and check its address. `GET /healthz` should return 204. A phone's `localhost` is the phone, not your computer; the quickstart's curl calls run on the server computer. |
| HTTP 401 from `/v1/devices` | Supply a fresh Firebase Auth ID token for this Firebase project. An OAuth/gcloud token or FCM token is not a Firebase ID token. Check expiration, revocation, disabled/deleted user state, and server Auth lookup permissions. |
| ADC works for Firestore but Auth still fails | Revocation checks also call Firebase Auth. Use the service-account identity described in the quickstart; ordinary gcloud end-user ADC has Firebase Auth restrictions. Check `roles/firebaseauth.viewer`. |
| HTTP 400 | Check the exact wire keys in [usage](usage.md). The API rejects unknown fields, malformed IDs, invalid permission/platform values, and oversized JSON. A registration uses `target`, not `token`; the server chooses the stored token/FID field. |
| HTTP 404 on heartbeat | The installation is missing, was pruned, or its address was transferred. Re-register under the currently authenticated user; do not keep retrying an old heartbeat. |
| HTTP 404 on a schedule route | Self-service routes are disabled by default. Use the trusted CLI, or deliberately enable `serve --client-schedules` behind application quotas. |
| HTTP 503 after authentication | Check Firestore access, app namespace, and server availability. `/healthz` does not test these dependencies. Do not log a raw request to diagnose it. |

The API's supplied rules do not grant Admin SDK permissions. Server-side access
uses IAM. In an existing project, check all matching Firestore rules before treating
the push namespace as server-only: adding a deny does not cancel an existing allow.
[Firebase Admin credentials](https://firebase.google.com/docs/admin/setup),
[ID token verification](https://firebase.google.com/docs/auth/admin/verify-id-tokens).

## Schedules that do not run

| Symptom | Check and action |
| --- | --- |
| `schedule` succeeds but no reminder arrives | `serve` only hosts the API. A separate `tick` invocation must run after the job is due. Verify your cron/Scheduler invocation actually succeeds. |
| Tick reports all zeroes | Inspect `apps/{app}/pushSchedules/{scheduleKey}`: `state` must be `ready`, `availableAt` must be due, and the worker must include that shard. A claimed job's `availableAt` is its lease expiry; a retry's is its next attempt time. |
| Index error / failed precondition | Deploy the composite index from `firestore.indexes.json` to the correct project/database and wait until it is ready. Check server IAM separately. |
| A reminder is one hour off or uses the old timezone | Device timezone and schedule timezone are distinct. Update the schedule's `daily.timezone` explicitly. Use an IANA name, not a fixed UTC offset. |
| A DST transition skips a reminder | A nonexistent spring-forward local time is skipped. During a repeated autumn hour, the scheduler selects the first occurrence once. |
| Daily schedule has `state: failed` | Five unresolved worker attempts stop the series. Inspect `lastError`, provider configuration, and eligible devices. Fix the cause, then replace the schedule through the trusted CLI/API. Replacement clears progress; do not treat it as a safe retry of an ambiguous prior send. |
| Backlog grows | A tick processes jobs sequentially and defaults to 100 per shard. Measure processing latency, retries, and oldest due job before increasing the limit or task count. Thirty-two shards do not require 32 billed worker tasks. See [GCP deployment](gcp.md). |

Daily schedules store real UTC timestamps for querying and local time/zone for
recurrence. They are not 15-minute buckets. The due query also includes overdue
work; a missed daily series produces at most one catch-up occurrence before moving
to the next future date. See [storage and delivery](architecture.md).

## FCM accepts a push but nothing appears

For a direct send, `accepted=1` is provider acceptance, not an OS display receipt.
For a schedule, `Completed: 1` means the job finished; a user with no eligible devices
can also complete without a send.

1. Verify the device belongs to the same Firebase project and the registration
   target is current. Only enabled, granted installations seen within 45 days are
   eligible. Heartbeats occur when the app runs, not automatically on push receipt.
2. On Android, check notification permission and the `reminders` channel's settings.
   Implement the messaging service and foreground presenter from the platform guide.
3. On iOS, check Firebase's APNs configuration, signing entitlements, and APNs token
   forwarding. Configure the foreground notification delegate. A raw Firebase
   Installations ID is not a Messaging-registered FID.
4. Check foreground/background state, Focus/notification settings, connectivity,
   and message TTL. Verify on a physical device; simulator tests cannot establish
   production FCM/APNs delivery.

The FCM adapter classifies failures into the following library categories:

| Category | Action |
| --- | --- |
| `unregistered` | The worker/example retires that installation. Register a fresh target from the device. |
| `permanent` | Check project/sender matching and payload validity. The target is not automatically deleted. |
| `transient` / `rate_limited` | Retries use bounded backoff; quota retries wait at least 60 seconds. Avoid immediate manual resend loops. |
| `unknown` | Includes APNs credential failures. Check provider configuration in your own protected tooling. The dispatcher does not retry unclassified top-level errors; scheduled jobs can retry across worker attempts. |

[FCM error reference](https://firebase.google.com/docs/cloud-messaging/error-codes).

## Duplicate notifications or unexpected sends

FCM and Firestore do not share a transaction. A crash after provider acceptance can
cause a retry to duplicate delivery. Each direct send is a new request, and replacing
a schedule clears its progress. Inspect the occurrence's `notification_id` and your
own sender history before retrying manually. The native libraries expose the ID;
persistent display deduplication is the host app's responsibility.

Await installation unbinding before signing out. An ownership transfer clears the
old owner's address, but cannot recall an already queued push. During a migration,
ensure only one delivery system owns each user's reminder occurrence.

## Asking for help

For a reproducible issue, include the reviewed commit, platform/OS and dependency
versions, command or route, HTTP status, aggregate tick report, and sanitized state
fields such as `state`, `dueAt`, `availableAt`, and `lastError`. Use invented user IDs
and omit tokens, credential files, provider message IDs, and message contents that
contain personal data. Use the [security reporting guidance](../SECURITY.md) for
vulnerabilities.
