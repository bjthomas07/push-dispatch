# Android

Two source modules: `notifications-core` and `notifications-firebase`. Android API
23+, compile SDK 36, Java 17, Kotlin 2.3.21, AGP 8.13.2, Firebase Messaging 25.1.2.
The included standalone Gradle build has exact pins and dependency lockfiles.
No Maven coordinates have been published.

Clone/vendor this repository at a reviewed commit. In the consuming settings file:

```kotlin
include(":notifications-core", ":notifications-firebase")
project(":notifications-core").projectDir = file("../push-dispatch/android/core")
project(":notifications-firebase").projectDir = file("../push-dispatch/android/firebase")
```

These source modules use the consuming build's `libs` catalog. Copy the required
aliases and pins from `gradle/libs.versions.toml`, declare the Android library and
Kotlin plugins with `apply false` in the root build, and depend on
`implementation(project(":notifications-firebase"))` in the app. Alternatively
build the AARs here and supply the corresponding transitive dependencies yourself.

The app owns `google-services.json`, the Google Services plugin, a
`FirebaseMessagingService`, POST_NOTIFICATIONS permission on Android 13+, channel
resources, navigation, and authenticated HTTP. Add the service to the app manifest
with the `com.google.firebase.MESSAGING_EVENT` intent filter and `exported=false`.

```kotlin
import io.github.bjthomas07.pushdispatch.core.*
import io.github.bjthomas07.pushdispatch.firebase.*

val targetProvider = FirebaseRegistrationTokenTargetProvider(com.google.firebase.messaging.FirebaseMessaging.getInstance())
val manager = NotificationInstallationManager(
    targetProvider = targetProvider,
    backend = backend,
    stateStore = SharedPreferencesNotificationInstallationStateStore(context),
)
val state = manager.bind(
    accountKey = currentUser.uid,
    metadata = NotificationInstallationMetadata(
        platform = "android", permission = NotificationPermission.GRANTED,
        timezone = java.util.TimeZone.getDefault().id, enabled = true,
    ),
)
```

Derive permission from the actual OS/channel state; the example's granted value
is illustrative. `backend` implements `NotificationInstallationBackend` using the
app's HTTP/auth stack. **Serialize enums using `wireValue`**, not Kotlin enum names.
For bind, send:

```kotlin
mapOf(
    "provider" to target.provider.wireValue,
    "targetType" to target.type.wireValue,
    "target" to target.value,
    "platform" to metadata.platform,
    "permission" to metadata.permission.wireValue,
    "timezone" to metadata.timezone,
    "enabled" to metadata.enabled,
)
```

Bind maps to `POST /v1/devices` and returns its `installationId`. Heartbeat sends
permission/timezone/enabled to `POST /v1/devices/{id}/heartbeat`; map a 404 to
`NotificationInstallationNotFoundException`. Unbind uses DELETE and accepts 204.
All requests use the currently owning user's verified Firebase Auth ID token.
[Complete JSON contract](../docs/usage.md).

Forward `FirebaseMessagingService.onNewToken` to the **same** provider singleton
via `onNewToken(token)`, then trigger the lifecycle bind path from an app-owned
coroutine scope. The provider fetches the current token explicitly during bind.
Await `manager.unbind(currentUser.uid)` before Firebase sign-out/account switching;
failed cleanup is retained and retried. Never erase the state to bypass that guard.

Use `FirebaseNotificationMessageParser.parse(remoteMessage)` for incoming payloads.
`FirebaseNotificationPresenter` renders foreground messages with an app-supplied
explicit launch Intent, icon, and notification channel. System-rendered background
notifications use the app's Firebase default channel/icon manifest metadata; handle
their extras on cold start and `onNewIntent`. Avoid rendering the same background
notification twice. Validate deep links against your app's allowed routes.
`FirebaseNotificationAnalytics` helps distinguish system and app-rendered opens.

```sh
# From repository root; ANDROID_HOME must point to the installed Android SDK
mise run android-test
mise run android-build
```

Exclude the `notification_installation_v1.xml` SharedPreferences file from Android
Auto Backup and device-transfer backup rules; a restored binding must not become
a second device using an old target. Use the corresponding filename if you provide
a custom preferences name. The app owns these backup rules.

The app must reconcile its Play Data Safety disclosure with Firebase collection.
Unit tests/builds do not verify real-device FCM delivery or cloud credentials.
