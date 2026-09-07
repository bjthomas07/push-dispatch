# Storage and delivery

## Boundaries

`push` defines messages, targets, and bounded dispatch. `push/fcm` translates those
values to the Firebase Admin SDK. `push/store` implements installation ownership
in Firestore. `scheduler` depends on `Store`, `Targets`, and `Sender` interfaces;
`scheduler/firestore` implements its persistence contract. `api` accepts a caller-
supplied authentication function. The CLI composes these for GCP/Firebase Auth.

An AWS host can run the Go packages as-is with workload identity for FCM. Replacing
Firestore with DynamoDB requires conditional claim/finish operations and an index
on due time per shard. EventBridge can invoke the worker. Those adapters are not
implemented or tested in this release. No cloud SDK is required to import the
core `push` or `scheduler` package.

## Firestore schema

All records are namespaced by app. Firebase Auth verifies the owner before writes.
Client Firestore access to this namespace is denied by the supplied rules.

| Path under `apps/{app}` | Contents |
|---|---|
| `users/{uid}` | Bounded `pushInstallations` map; maximum 20 installations |
| `pushTargetOwners/{installationId}` | `uid`, `updatedAt`; unique owner per target |
| `pushSchedules/{scheduleKey}` | Schedule, current occurrence, retry/lease state |

`installationId = SHA256(provider + NUL + targetType + NUL + target)`. It is a
stable pseudonymous identifier, not a secret or an authorization credential.
Target addresses remain sensitive, even when their hashes are safe for diagnostics.

An installation stores `provider`, `targetType`, `fid` **or** `token`, `platform`,
`permission`, `timezone`, optional `locale`/`appVersion`, `enabled`, `createdAt`,
`updatedAt`, `lastSeenAt`, `disabledAt`, and optional `delivered` markers. A transaction
moves a target between owners and clears the old owner's address. Heartbeats cannot
revive an address cleared during a transfer. Re-registration preserves creation
and delivery metadata. Disabled installations do not fall back to another vendor.

Only enabled installations with granted permission and a heartbeat in the last
45 days are eligible. On registration/heartbeat, the store prunes entries unseen
for 90 days, entries disabled for 30 days, and oldest entries over the 20-device
limit. Pruning is opportunistic, not a background data-deletion guarantee. Apply
your account deletion and retention jobs to users, owner records, and schedules.
The embedded store supports a transactional account-deletion fence through Config.

`scheduleKey = SHA256(recipientId + NUL + id)`. A schedule contains:

```text
id, recipientId, daily? {localTime, timezone}, message
state: ready | done | failed | canceled
shard: 0..31
 dueAt: timestamp of this occurrence
 availableAt: timestamp for the next claim (due, retry, or lease expiry)
leaseToken, attempts, completed: [installationId], lastError
invalidTargets: [installationId], hasPermanentFailure, updatedAt
```

Fields are explicitly tagged in Go; timestamps are Firestore timestamps, not
HHMM UTC slots or ISO strings. The API accepts HHMM local time plus an IANA zone
and computes the next real instant. DST gaps skip the nonexistent daily time;
DST folds use the first occurrence only. Missed daily backlogs produce at most one
catch-up occurrence before scheduling the next future day. Daily series stop in
`failed` after five unresolved worker attempts and require an operator to requeue.

## Claims, retries, and limits

Each worker queries `state == ready AND shard == N AND availableAt <= now`, ordered
by `availableAt`, with a limit. The composite index is in `firestore.indexes.json`.
A Firestore transaction rechecks eligibility and assigns a random lease token.
Completion compares that token before saving progress. Cancellation or replacing
a schedule clears the token, so old workers cannot overwrite the replacement.

Workers claim one job at a time. A five-minute lease covers a four-minute send
budget and a separate 20-second acknowledgement budget. Multiple workers may
process a shard; a race loser can leave remaining jobs for the next tick. Cloud Run
task counts from 1 through 32 partition all 32 shards without gaps. Each tick bounds
jobs per shard; increase invocation frequency or worker count as backlog grows.
The implementation has concurrency tests, not a production throughput benchmark.

The dispatcher deduplicates addresses, uses batches of at most 500 targets, and
bounds concurrent batches to 1 by default (maximum 4). It retries only transient
and quota failures, excluding acknowledged successes. Quota retries wait at least
60 seconds with positive jitter. A worker records completed targets and retries
unresolved work with 1/2/4/8-minute delays, up to five worker attempts. Each worker
attempt can include three provider attempts. Permanent failures are reported;
only unregistered addresses are automatically retired. Failed retirement is persisted
and retried without resending an acknowledged target.

**Delivery is at least once at the attempt boundary, with best-effort duplicate
suppression.** There is no atomic transaction across FCM and Firestore. A crash,
ambiguous provider response, or lost acknowledgement after FCM accepted a push can
cause a duplicate. Successful targets persisted for an occurrence are excluded
from later attempts. Stable `notification_id` values support app-level deduplication;
the native libraries expose that ID but do not promise persistent display dedupe.
Cancellation, logout, and permission changes cannot recall an in-flight push.

The sample emits aggregate tick reports and sanitized error categories. Monitor
backlog age, `failed` schedules, retry counts, IAM failures, and provider quotas.
The adapter scans due jobs, not all users; each job reads one bounded installation
map. Idle polling still costs reads, and every delivery has claim/acknowledgement
writes. For large campaigns, enqueue bounded recipient jobs from your own producer;
there is no unbounded broadcast scan or marketing segmentation engine here.
