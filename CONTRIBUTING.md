# Contributing

Keep core types independent of cloud SDKs. Put storage and provider integrations
in adapters. Keep app branding, account identifiers, credentials, and production
fixtures out of changes. Pin direct dependencies and commit resolved/lock files.

```sh
mise trust
mise install
mise run fmt
mise run test
mise run vet
mise install java@21.0.2
mise run test-emulator
mise run android-test
mise run android-build
mise run apple-test
mise run audit
mise run audit-native
```

Android requires SDK platform 36 and build-tools 35.0.0, with `ANDROID_HOME` set.
Apple tests require Xcode and an iOS simulator; set `IOS_DESTINATION` if needed.
Emulator tests use only `demo-push-dispatch` and skip in the ordinary Go suite when
`FIRESTORE_EMULATOR_HOST` is absent. `test-emulator` starts an isolated local emulator.

Tests must cover behavior: target rotation, failed logout, owner-safe cleanup,
concurrent claims, expired leases, partial sends, retries, cancellation, and DST.
Provider acceptance does not prove real-device delivery. Document that distinction
when changing platform integrations. Avoid broad platform abstractions until a
second adapter demonstrates the need.

Release process: review the source and NOTICE, run all checks, confirm physical
Android/iOS delivery in a test Firebase project, then create a version tag. Go and
Swift consumers can pin commits until tagged releases exist. Android currently
supports included source modules and locally built AARs; no Maven package has been
published. Changing repository visibility is a separate maintainer decision.
