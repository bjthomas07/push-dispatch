# iOS

Swift Package Manager products: `PushDispatchCore` and `PushDispatchFirebase`.
iOS 15+, Swift tools 6.1; Firebase Messaging is pinned to **12.16.0**. App workspaces
must resolve the same Firebase version. Commit their `Package.resolved` too.

Add the public repository directly in Xcode or Package.swift. Pin a reviewed
commit until release tags are published:

```swift
.package(
    url: "https://github.com/bjthomas07/push-dispatch",
    revision: "FULL_REVIEWED_COMMIT"
)
```

The root Swift manifest references the sources in `ios/`; Go and Swift consumers
can both use this repository without vendoring a second checkout.

Add both products to the app target. The app owns its Firebase configuration,
`GoogleService-Info.plist`, APNs capability/entitlements, delegates, authenticated
HTTP transport, and navigation. Enable Push Notifications and background remote
notifications in the consuming target; configure the APNs key in Firebase.

In the app's Info.plist:

```xml
<key>FirebaseMessagingInstallationIdEnabled</key><true/>
<key>FirebaseMessagingAutoInitEnabled</key><false/>
```

Set Firebase auto-init off before Firebase configuration using
`FirebaseMessagingNotificationProvider.prepareForFirebaseConfiguration()`, then
configure Firebase and the provider. Choose one APNs delegate integration strategy
and forward the callbacks below consistently with the app's Firebase swizzling
configuration; do not create a second competing Messaging delegate.

```swift
import PushDispatchCore
import PushDispatchFirebase

let lifecycle = NotificationInstallationLifecycle(
    stateStore: UserDefaultsNotificationInstallationStateStore()
)
let provider = FirebaseMessagingNotificationProvider()
provider.configure() // after FirebaseApp.configure()

// During authenticated setup, after resolving any pending old-account unbind:
let target = try await provider.startRegistrationAndWaitForTarget()
let request = NotificationRegistrationRequest(
    target: target, platform: .iOS, permission: await provider.permission(),
    timezone: TimeZone.current.identifier, enabled: true
)
try await lifecycle.bindUser(userID, request: request, using: transport)
```

`transport` implements `NotificationInstallationTransport` with your authenticated
HTTP client. Use JSONEncoder/JSONDecoder for the package's request/receipt models.
Map register to `POST /v1/devices`, heartbeat to `POST /v1/devices/{id}/heartbeat`,
and unregister to `DELETE /v1/devices/{id}`. A heartbeat 404 must throw
`NotificationInstallationTransportError.installationNotFound`; other errors must
propagate. Do not sign out the owning Firebase user until unbind has completed.
See [the shared API contract](../docs/usage.md).

Forward OS callbacks:

| App callback | Provider method |
|---|---|
| APNs registration | `setAPNSToken(_:)` |
| APNs registration error | `didFailToRegisterForRemoteNotifications(_:)` |
| Background delivery / foreground presentation | `handleReceivedNotification(userInfo:)` |
| Notification tap | `handleOpenedNotification(userInfo:actionIdentifier:)` |

Keep a strong provider reference and assign its weak `eventDelegate` to an
app-owned `NotificationEventDelegate`. Registration callbacks should enter the
same lifecycle bind path. Received/open callbacks expose a typed payload; validate
the URL's scheme and route before navigating. The app decides foreground presentation.

The lifecycle retains failed cleanup and blocks cross-account binding. If an iOS
unbind fails, suspend provider registration before using the lifecycle's documented
forget-after-invalidation recovery; if both fail, keep the pending owner binding.
Never erase local binding state as a substitute for authenticated unbinding.

The included privacy manifest declares UserDefaults access and linked Device ID
collection for app functionality. Reconcile the consuming app's privacy manifest
and store disclosure with its Firebase setup. The package does not upload analytics
to a project-owned service; event callbacks belong to the app.

```sh
# From repository root; override for an installed simulator as needed
IOS_DESTINATION='platform=iOS Simulator,name=iPhone 17 Pro' mise run ios-test
```

The package tests validate lifecycle and callback behavior. Production APNs/FCM
registration, delivery, and taps still need physical-device verification.
