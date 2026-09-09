# push-dispatch

Self-hosted mobile push notifications and scheduled reminders. Go on the server,
Swift and Kotlin on devices. FCM delivers; your infrastructure owns registration,
schedules, and delivery state.

**Early development · MIT licensed.**

**[Send your first reminder](docs/quickstart.md)** — register a device, send a push,
and create and cancel a daily schedule in your own Firebase test project.

- Native registration, token/FID refresh, permission sync, 24-hour heartbeats,
  and account-safe unbinding.
- Immediate sends, one-off schedules, and daily reminders in an IANA timezone.
- Firestore transactions for target ownership and worker claims; indexed,
  sharded due queries; bounded retries and per-target progress.
- Portable Go interfaces for storage and delivery. GCP is implemented today;
  AWS adapters are welcome.

```sh
# Go 1.26.6, or install the pinned tools with mise
git clone https://github.com/bjthomas07/push-dispatch.git
cd push-dispatch
mise trust
mise install
mise run test
mise run build
./bin/push-dispatch --help
```

The [quickstart](docs/quickstart.md) covers credentials, registration, and expected
results. `serve` runs the device API; `tick` processes due schedules. A recurring
worker is needed to deliver reminders when this terminal is closed.

[Runnable Go example](examples/send/main.go) · [Usage and JSON](docs/usage.md) · [Apple](apple/README.md) ·
[Android](android/README.md) · [GCP deployment](docs/gcp.md) ·
[Troubleshooting](docs/troubleshooting.md) ·
[Storage and delivery guarantees](docs/architecture.md) · [Contributing](CONTRIBUTING.md)

Go and Swift consumers can pin a reviewed commit; there are no release tags yet.
Android is distributed as source modules or locally built AARs. Automated Go,
Android, and iOS tests run in [CI](https://github.com/bjthomas07/push-dispatch/actions/workflows/ci.yml).
Production throughput and physical-device delivery have not been validated for
this extracted release; verify them in your app before enabling real users.

## Why this exists

First there was Parse.com. Then came another migration. OneSignal promoted a free
alternative, including a Parse migration tool announced by its cofounder. Years
later, its announced free-plan change caps mobile push and in-app messaging at
1,000 monthly active users: September 1, 2026 for new customers, October 1 for
existing customers. [The history and sources](docs/motivation.md).

We are spending engineering time on another migration and another round of device
testing because a pricing promise changed. That is frustrating work for a basic
capability our apps already had. This project puts that capability in a library
we can inspect, run, and keep.

There is no per-MAU license fee, hosted account, advertising SDK, or library-owned
telemetry endpoint. FCM and your chosen infrastructure still have their own
terms, quotas, and operating costs. This release covers mobile push and reminders;
marketing dashboards, email, web push, and journey builders are outside its scope.
