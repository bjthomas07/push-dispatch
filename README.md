# push-dispatch

Self-hosted mobile push notifications and scheduled reminders. Go on the server,
Swift and Kotlin on devices. FCM delivers; your infrastructure owns registration,
schedules, and delivery state.

**Early development · MIT licensed · repository currently private.**

- Native registration, token/FID refresh, permission sync, 24-hour heartbeats,
  and account-safe unbinding.
- Immediate sends, one-off schedules, and daily reminders in an IANA timezone.
- Firestore transactions for target ownership and worker claims; indexed,
  sharded due queries; bounded retries and per-target progress.
- Portable Go interfaces for storage and delivery. GCP is implemented today;
  AWS adapters are welcome.

```sh
# Go 1.26.6, or install the pinned tools with mise
mise trust
mise install
mise run test
mise run build
./bin/push-dispatch --help
```

```sh
export GOOGLE_CLOUD_PROJECT=your-project
export PUSH_APP=your-app
gcloud auth application-default login
./bin/push-dispatch serve
```

Register a device through the authenticated API, then schedule a reminder:

```sh
./bin/push-dispatch schedule < examples/daily.json
./bin/push-dispatch tick
```

[Usage and JSON](docs/usage.md) · [Apple](apple/README.md) ·
[Android](android/README.md) · [GCP deployment](docs/gcp.md) ·
[Storage and delivery guarantees](docs/architecture.md) · [Contributing](CONTRIBUTING.md)

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
