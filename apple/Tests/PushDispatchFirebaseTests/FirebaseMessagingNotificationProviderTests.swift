import PushDispatchCore
import UserNotifications
import XCTest
@testable import PushDispatchFirebase

final class FirebaseMessagingNotificationProviderTests: XCTestCase {
    @MainActor
    func testPrepareForFirebaseConfigurationDisablesPersistedAutoInit() throws {
        let suiteName = "FirebaseMessagingNotificationProviderTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
        defer { defaults.removePersistentDomain(forName: suiteName) }
        defaults.set(
            true,
            forKey: FirebaseMessagingNotificationProvider.autoInitDefaultsKey
        )

        FirebaseMessagingNotificationProvider.prepareForFirebaseConfiguration(
            defaults: defaults
        )

        XCTAssertFalse(
            defaults.bool(
                forKey: FirebaseMessagingNotificationProvider.autoInitDefaultsKey
            )
        )
    }

    @MainActor
    func testPermissionMapping() {
        XCTAssertEqual(
            FirebaseMessagingNotificationProvider.permission(for: .authorized),
            .granted
        )
        XCTAssertEqual(
            FirebaseMessagingNotificationProvider.permission(for: .provisional),
            .granted
        )
        XCTAssertEqual(
            FirebaseMessagingNotificationProvider.permission(for: .ephemeral),
            .granted
        )
        XCTAssertEqual(
            FirebaseMessagingNotificationProvider.permission(for: .denied),
            .denied
        )
        XCTAssertEqual(
            FirebaseMessagingNotificationProvider.permission(for: .notDetermined),
            .unknown
        )
    }

    @MainActor
    func testReceivedNotificationIsForwardedToMessagingAnalytics() {
        var reportedMessages: [[AnyHashable: Any]] = []
        let provider = FirebaseMessagingNotificationProvider(
            notificationCenter: { XCTFail("delivery hooks must not resolve the permission center"); return .current() },
            analyticsReporter: { reportedMessages.append($0) }
        )

        provider.handleReceivedNotification(userInfo: ["gcm.message_id": "received-message"])

        XCTAssertEqual(reportedMessages.count, 1)
        XCTAssertEqual(reportedMessages[0]["gcm.message_id"] as? String, "received-message")
    }

    @MainActor
    func testOpenedNotificationIsForwardedToMessagingAnalytics() {
        var reportedMessages: [[AnyHashable: Any]] = []
        let eventRecorder = NotificationEventRecorder()
        let provider = FirebaseMessagingNotificationProvider(
            notificationCenter: { XCTFail("delivery hooks must not resolve the permission center"); return .current() },
            analyticsReporter: { reportedMessages.append($0) }
        )
        provider.eventDelegate = eventRecorder

        provider.handleOpenedNotification(userInfo: [
            "gcm.message_id": "opened-message",
            "notification_id": "logical-message",
        ])

        XCTAssertEqual(reportedMessages.count, 1)
        XCTAssertEqual(reportedMessages[0]["gcm.message_id"] as? String, "opened-message")
        XCTAssertEqual(eventRecorder.openedEvents.first?.provider, .fcm)
        XCTAssertEqual(eventRecorder.openedEvents.first?.presentation, .system)
    }

    @MainActor
    func testRegistrationTargetWaitsForFIDCallback() async throws {
        let waiter = FirebaseMessagingRegistrationTargetWaiter()
        let target = NotificationTarget(
            provider: .fcm,
            type: .firebaseInstallationID,
            value: "fid-a"
        )
        let pending = Task { @MainActor in
            try await waiter.wait(timeoutNanoseconds: 1_000_000_000)
        }
        for _ in 0..<100 where waiter.pendingCount == 0 { await Task.yield() }

        XCTAssertEqual(waiter.pendingCount, 1)
        waiter.resolve(target)

        let resolvedTarget = try await pending.value
        XCTAssertEqual(resolvedTarget, target)
        XCTAssertEqual(waiter.pendingCount, 0)
    }

    @MainActor
    func testRegistrationTargetTimeoutIsRetrySafe() async throws {
        let waiter = FirebaseMessagingRegistrationTargetWaiter()

        do {
            _ = try await waiter.wait(timeoutNanoseconds: 0)
            XCTFail("Expected registration target timeout")
        } catch let error as FirebaseMessagingNotificationProviderError {
            XCTAssertEqual(error, .registrationTargetTimedOut)
        }
        XCTAssertEqual(waiter.pendingCount, 0)

        let target = NotificationTarget(
            provider: .fcm,
            type: .firebaseInstallationID,
            value: "fid-retry"
        )
        let retry = Task { @MainActor in
            try await waiter.wait(timeoutNanoseconds: 1_000_000_000)
        }
        for _ in 0..<100 where waiter.pendingCount == 0 { await Task.yield() }
        waiter.resolve(target)

        let retryTarget = try await retry.value
        XCTAssertEqual(retryTarget, target)
    }
}

@MainActor
private final class NotificationEventRecorder: NotificationEventDelegate {
    private(set) var openedEvents: [NotificationOpenEvent] = []

    func notificationRegistrationDidUpdate(_: NotificationTarget) {}

    func notificationDidOpen(_ event: NotificationOpenEvent) {
        openedEvents.append(event)
    }
}
