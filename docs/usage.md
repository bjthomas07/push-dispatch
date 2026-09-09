# Usage

## Go library

The public Go module lives at the repository root. No release tag is published
yet; pin a reviewed commit from the repository history:

```sh
go get github.com/bjthomas07/push-dispatch@FULL_REVIEWED_COMMIT
```

For a complete program with imports, target lookup, partial-failure handling, and
invalid-token cleanup, see [the send example](../examples/send/main.go). After
[registering a device](quickstart.md), run it from the checkout:

```sh
# Uses GOOGLE_CLOUD_PROJECT, PUSH_APP, PUSH_USER_ID, and server ADC credentials.
mise run example-send
```

The following excerpt shows just the dispatch call:

```go
sender, err := fcm.New(ctx, projectID)
if err != nil { return err }
dispatcher, err := push.NewDispatcher(sender, 1)
if err != nil { return err }
result, err := dispatcher.Send(ctx, push.Message{
    Title: "Hello", Body: "Your reminder is ready.", TTL: time.Hour,
    Data: map[string]string{"deep_link": "example://today"},
}, targets)
// Inspect result.Results even when err is non-nil; some targets may have succeeded.
```

Imports: `github.com/bjthomas07/push-dispatch/push` and
`github.com/bjthomas07/push-dispatch/push/fcm`. Keep provider errors and target
addresses out of logs. Only unregistered targets should be retired automatically;
a permanent payload/configuration error does not prove a token is invalid.

## CLI

All commands accept `--project PROJECT --app APP`, or the environment variables
`GOOGLE_CLOUD_PROJECT` and `PUSH_APP`. Application Default Credentials authenticate
the server; mobile clients use Firebase Auth ID tokens. CLI writes are trusted
administrative operations.

```sh
./bin/push-dispatch --help
./bin/push-dispatch serve --listen :8080
./bin/push-dispatch schedule < examples/daily.json
./bin/push-dispatch tick --limit 100
./bin/push-dispatch tick --shard 7 --limit 100
./bin/push-dispatch cancel --user USER_UID --id morning
```

A one-off schedule replaces `daily` with `"at":"2026-10-01T12:00:00Z"`.
`at` must contain an offset. Past one-off times run on the next tick. PUT/CLI
schedule replaces the same user's schedule ID, clearing its delivery history;
replacing an already sent one-off intentionally permits another send.

Immediate sends use stdin, so tokens and content need not enter shell history:

```sh
./bin/push-dispatch send <<'JSON'
{"recipientId":"USER_UID","message":{"title":"Hello","body":"Your reminder is ready."}}
JSON
```

`send` prints accepted/failed counts and exits nonzero on incomplete delivery.
`tick` prints per-shard claimed/completed/retrying/failed counts. A failed schedule
is persisted for inspection; infrastructure errors cause a nonzero exit. Provider
acceptance is not a device-delivery receipt.

## Device synchronization

The native lifecycle invokes these routes using `Authorization: Bearer ID_TOKEN`.
No UID is accepted in request bodies. The standalone server creates a minimal user
document on the first verified registration. When embedding the Firestore store,
`CreateUsers` defaults to false so existing account lifecycle rules remain yours.

| Method | Route | Result |
|---|---|---|
| POST | `/v1/devices` | `{"installationId":"64-character-hash"}` |
| POST | `/v1/devices/{id}/heartbeat` | Same receipt; 404 triggers re-registration |
| DELETE | `/v1/devices/{id}` | 204, including already-absent installations |

Registration body (identical wire keys on both platforms):

```json
{
  "provider": "fcm",
  "targetType": "token",
  "target": "FCM_REGISTRATION_TOKEN",
  "platform": "android",
  "permission": "granted",
  "timezone": "America/New_York",
  "enabled": true
}
```

For iOS FID mode use `targetType: "fid"`, `platform: "ios"` and the FID returned
by Firebase Messaging registration. Permission is `granted`, `denied`, or `unknown`.
A bare Firebase Installations identifier is insufficient unless Messaging has
registered it for delivery.

Heartbeat body:

```json
{"permission":"granted","timezone":"Europe/London","enabled":true}
```

Bind on login, foreground, permission changes, timezone changes, and provider target
callbacks. Unchanged state is throttled to one heartbeat per 24 hours; changed
targets or metadata sync immediately. **Await unbind before signing out** while the
old identity can still authenticate. On failure, retain local binding state and
retry; the lifecycle refuses an unsafe account switch. iOS also supports provider
registration suspension. A previously queued notification cannot be recalled.

## Reminder synchronization

The optional schedule API authenticates the same way and targets only the caller:

```http
PUT /v1/schedules/morning
Authorization: Bearer ID_TOKEN
Content-Type: application/json

{"daily":{"localTime":830,"timezone":"Europe/London"},"message":{"title":"Hello","body":"Your reminder is ready."}}
```

`GET /v1/schedules/morning` returns `id`, `daily`, `dueAt`, `state`, and `message`.
DELETE returns 204 and cancels the schedule. Schedule data is written through the
backend, never by the mobile Firestore SDK. On a timezone change, also PUT the
reminder with its updated timezone; device heartbeat does not alter reminder
preferences. IDs are opaque and can be supplied by your app's settings catalog.

Mount `api.Schedules` behind your app's rate and schedule-count limits. The CLI's
standalone server exposes it only with `--client-schedules`; otherwise create
schedules from trusted server code or the administrative CLI.

## Message payload

`message` accepts `title`, `body`, string-valued `data`, `apple`, `android`,
`analytics`, and `ttlNanoseconds` (Go duration units: one hour = 3600000000000).
At least title or body is required. The 3 KiB library payload budget leaves room
for provider overhead; TTL is capped at 28 days. Scheduled messages default to a
one-hour TTL. Direct sends with zero TTL use the provider's default retention.

The scheduler supplies stable `notification_id`, `schema_version`, `app`, `kind`,
and `analytics_label` data keys. Do not set those keys in scheduled `data`.
`deep_link` and `content_id` are conventional optional app fields. Native parsers
also accept legacy camel-case spellings; all custom string data remains available.
Validate deep links against your app's permitted schemes/routes before navigation.
Analytics callbacks let the app record opens; the library has no telemetry service.
