import PushDispatchCore
import FirebaseMessaging
import Foundation
import UIKit
import UserNotifications

public enum FirebaseMessagingNotificationProviderError: LocalizedError, Sendable, Equatable {
    case installationIDModeDisabled
    case registrationTargetTimedOut

    public var errorDescription: String? {
        switch self {
        case .installationIDModeDisabled:
            return "FirebaseMessagingInstallationIdEnabled must be YES in Info.plist."
        case .registrationTargetTimedOut:
            return "Firebase Messaging did not return an installation ID in time."
        }
    }
}

@MainActor
final class FirebaseMessagingRegistrationTargetWaiter {
    private struct PendingWaiter {
        let continuation: CheckedContinuation<NotificationTarget, any Error>
        let timeoutTask: Task<Void, Never>
    }

    private var pending: [UUID: PendingWaiter] = [:]

    var pendingCount: Int { pending.count }

    func wait(timeoutNanoseconds: UInt64) async throws -> NotificationTarget {
        let id = UUID()
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation {
                (continuation: CheckedContinuation<NotificationTarget, any Error>) in
                let timeoutTask = Task { @MainActor [weak self] in
                    do {
                        try await Task.sleep(nanoseconds: timeoutNanoseconds)
                    } catch {
                        return
                    }
                    self?.fail(
                        id,
                        error: FirebaseMessagingNotificationProviderError.registrationTargetTimedOut
                    )
                }
                pending[id] = PendingWaiter(
                    continuation: continuation,
                    timeoutTask: timeoutTask
                )
            }
        } onCancel: {
            Task { @MainActor [weak self] in
                self?.fail(id, error: CancellationError())
            }
        }
    }

    func resolve(_ target: NotificationTarget) {
        let waiters = Array(pending.values)
        pending.removeAll()
        for waiter in waiters {
            waiter.timeoutTask.cancel()
            waiter.continuation.resume(returning: target)
        }
    }

    func failAll(_ error: any Error) {
        let waiters = Array(pending.values)
        pending.removeAll()
        for waiter in waiters {
            waiter.timeoutTask.cancel()
            waiter.continuation.resume(throwing: error)
        }
    }

    private func fail(_ id: UUID, error: any Error) {
        guard let waiter = pending.removeValue(forKey: id) else { return }
        waiter.timeoutTask.cancel()
        waiter.continuation.resume(throwing: error)
    }
}

/// Firebase Messaging adapter for Apple platforms. It owns provider-specific
/// registration and translates callbacks into the Core target/event contract.
@MainActor
public final class FirebaseMessagingNotificationProvider: NSObject {
    typealias AnalyticsReporter = ([AnyHashable: Any]) -> Void

    static let autoInitDefaultsKey = "com.firebase.messaging.auto-init.enabled"
    private static let registrationTargetTimeoutNanoseconds: UInt64 = 10_000_000_000

    public weak var eventDelegate: (any NotificationEventDelegate)?
    public private(set) var currentTarget: NotificationTarget?

    private let notificationCenter: () -> UNUserNotificationCenter
    private let analyticsReporter: AnalyticsReporter
    private let registrationTargetWaiter = FirebaseMessagingRegistrationTargetWaiter()

    public convenience init(notificationCenter: UNUserNotificationCenter = .current()) {
        self.init(notificationCenter: { notificationCenter }) { userInfo in
            _ = Messaging.messaging().appDidReceiveMessage(userInfo)
        }
    }

    init(
        notificationCenter: @escaping () -> UNUserNotificationCenter,
        analyticsReporter: @escaping AnalyticsReporter
    ) {
        self.notificationCenter = notificationCenter
        self.analyticsReporter = analyticsReporter
        super.init()
    }

    public static func prepareForFirebaseConfiguration(
        defaults: UserDefaults = .standard
    ) {
        defaults.set(false, forKey: autoInitDefaultsKey)
    }

    /// Call after `FirebaseApp.configure()`. Auto-init should be disabled in the
    /// app plist so the owning lifecycle can check for a pending account unbind
    /// before activating this installation.
    public func configure() {
        Messaging.messaging().delegate = self
    }

    public var isInstallationIDModeEnabled: Bool {
        Messaging.messaging().isInstallationIdEnabled
    }

    public func permission() async -> NotificationPermission {
        let settings = await notificationCenter().notificationSettings()
        return Self.permission(for: settings.authorizationStatus)
    }

    @discardableResult
    public func requestPermission(
        options: UNAuthorizationOptions = [.alert, .badge, .sound]
    ) async throws -> NotificationPermission {
        _ = try await notificationCenter().requestAuthorization(options: options)
        UIApplication.shared.registerForRemoteNotifications()
        return await permission()
    }

    /// Enables FCM auto-init and explicitly asks for registration. The explicit
    /// call is idempotent and guarantees a delegate callback even if already
    /// registered.
    public func startRegistration() async throws {
        guard isInstallationIDModeEnabled else {
            throw FirebaseMessagingNotificationProviderError.installationIDModeDisabled
        }
        let messaging = Messaging.messaging()
        messaging.isAutoInitEnabled = true
        UIApplication.shared.registerForRemoteNotifications()
        try await withCheckedThrowingContinuation {
            (continuation: CheckedContinuation<Void, any Error>) in
            messaging.register { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: ())
                }
            }
        }
    }

    public func startRegistrationAndWaitForTarget() async throws -> NotificationTarget {
        try await startRegistration()
        if let currentTarget { return currentTarget }
        return try await registrationTargetWaiter.wait(
            timeoutNanoseconds: Self.registrationTargetTimeoutNanoseconds
        )
    }

    /// Stops automatic re-registration and invalidates the current FCM
    /// registration. Used as a privacy quarantine when backend unbind fails.
    public func suspendRegistration() async throws {
        let messaging = Messaging.messaging()
        messaging.isAutoInitEnabled = false
        try await withCheckedThrowingContinuation {
            (continuation: CheckedContinuation<Void, any Error>) in
            messaging.unregister { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: ())
                }
            }
        }
        currentTarget = nil
    }

    public func setAPNSToken(_ token: Data) {
        Messaging.messaging().apnsToken = token
    }

    public func didFailToRegisterForRemoteNotifications(_ error: Error) {
        currentTarget = nil
        registrationTargetWaiter.failAll(error)
    }

    public func handleReceivedNotification(userInfo: [AnyHashable: Any]) {
        analyticsReporter(userInfo)
        eventDelegate?.notificationDidReceive(NotificationPayload(userInfo: userInfo))
    }

    public func handleOpenedNotification(
        userInfo: [AnyHashable: Any],
        actionIdentifier: String? = nil
    ) {
        analyticsReporter(userInfo)
        let event = NotificationOpenEvent(
            payload: NotificationPayload(userInfo: userInfo),
            provider: .fcm,
            presentation: .system,
            actionIdentifier: actionIdentifier
        )
        eventDelegate?.notificationDidOpen(event)
    }

    public static func permission(
        for authorizationStatus: UNAuthorizationStatus
    ) -> NotificationPermission {
        switch authorizationStatus {
        case .authorized, .provisional, .ephemeral:
            return .granted
        case .denied:
            return .denied
        case .notDetermined:
            return .unknown
        @unknown default:
            return .unknown
        }
    }

    private func updateRegistration(_ target: NotificationTarget) {
        currentTarget = target
        registrationTargetWaiter.resolve(target)
        eventDelegate?.notificationRegistrationDidUpdate(target)
    }

    private func invalidateRegistration(_ target: NotificationTarget) {
        if currentTarget == target {
            currentTarget = nil
        }
        eventDelegate?.notificationRegistrationDidInvalidate(target)
    }
}

extension FirebaseMessagingNotificationProvider: MessagingDelegate {
    public nonisolated func messaging(
        _ messaging: Messaging,
        didReceiveRegistration installationID: String?
    ) {
        guard let installationID, !installationID.isEmpty else { return }
        let target = NotificationTarget(
            provider: .fcm,
            type: .firebaseInstallationID,
            value: installationID
        )
        Task { @MainActor [weak self] in
            self?.updateRegistration(target)
        }
    }

    public nonisolated func messaging(
        _ messaging: Messaging,
        didReceiveRegistrationToken registrationToken: String?
    ) {
        guard let registrationToken, !registrationToken.isEmpty else { return }
        let target = NotificationTarget(
            provider: .fcm,
            type: .registrationToken,
            value: registrationToken
        )
        Task { @MainActor [weak self] in
            guard let self, !self.isInstallationIDModeEnabled else { return }
            self.updateRegistration(target)
        }
    }

    public nonisolated func messaging(
        _ messaging: Messaging,
        didUnregister installationID: String
    ) {
        let target = NotificationTarget(
            provider: .fcm,
            type: .firebaseInstallationID,
            value: installationID
        )
        Task { @MainActor [weak self] in
            self?.invalidateRegistration(target)
        }
    }
}
