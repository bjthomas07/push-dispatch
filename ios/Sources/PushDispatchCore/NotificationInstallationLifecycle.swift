import Foundation

public enum NotificationBindingPhase: String, Codable, Sendable {
    case bound
    case pendingUnbind
}

public struct NotificationInstallationBinding: Codable, Equatable, Sendable {
    public let userID: String
    public let installationID: String
    public let target: NotificationTarget
    public let metadata: NotificationInstallationMetadata?
    public let lastHeartbeatAt: Date?
    public let phase: NotificationBindingPhase
    public let pendingCleanupInstallationIDs: [String]

    public init(
        userID: String,
        installationID: String,
        target: NotificationTarget,
        metadata: NotificationInstallationMetadata? = nil,
        lastHeartbeatAt: Date? = nil,
        phase: NotificationBindingPhase = .bound,
        pendingCleanupInstallationIDs: [String] = []
    ) {
        self.userID = userID
        self.installationID = installationID
        self.target = target
        self.metadata = metadata
        self.lastHeartbeatAt = lastHeartbeatAt
        self.phase = phase
        self.pendingCleanupInstallationIDs = pendingCleanupInstallationIDs
    }

    private enum CodingKeys: String, CodingKey {
        case userID
        case installationID
        case target
        case metadata
        case lastHeartbeatAt
        case phase
        case pendingCleanupInstallationIDs
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        userID = try container.decode(String.self, forKey: .userID)
        installationID = try container.decode(String.self, forKey: .installationID)
        target = try container.decode(NotificationTarget.self, forKey: .target)
        metadata = try container.decodeIfPresent(
            NotificationInstallationMetadata.self,
            forKey: .metadata
        )
        lastHeartbeatAt = try container.decodeIfPresent(
            Date.self,
            forKey: .lastHeartbeatAt
        )
        phase = try container.decodeIfPresent(
            NotificationBindingPhase.self,
            forKey: .phase
        ) ?? .bound
        pendingCleanupInstallationIDs = try container.decodeIfPresent(
            [String].self,
            forKey: .pendingCleanupInstallationIDs
        ) ?? []
    }
}

public protocol NotificationInstallationStateStore: Sendable {
    func load() async -> NotificationInstallationBinding?
    func save(_ binding: NotificationInstallationBinding) async
    func clear() async
}

public actor UserDefaultsNotificationInstallationStateStore:
    NotificationInstallationStateStore
{
    private let defaults: UserDefaults
    private let key: String
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    public init(
        suiteName: String? = nil,
        key: String = "PushDispatch.installationBinding"
    ) {
        if let suiteName, let suite = UserDefaults(suiteName: suiteName) {
            defaults = suite
        } else {
            defaults = .standard
        }
        self.key = key
    }

    public func load() -> NotificationInstallationBinding? {
        guard let data = defaults.data(forKey: key) else { return nil }
        return try? decoder.decode(NotificationInstallationBinding.self, from: data)
    }

    public func save(_ binding: NotificationInstallationBinding) {
        guard let data = try? encoder.encode(binding) else { return }
        defaults.set(data, forKey: key)
    }

    public func clear() {
        defaults.removeObject(forKey: key)
    }
}

public enum NotificationInstallationLifecycleError: Error, Equatable, Sendable {
    case bindingBelongsToAnotherUser
    case pendingUnbind
    case userMismatch
}

private actor NotificationInstallationOperationLock {
    private var isLocked = false
    private var waiters: [CheckedContinuation<Void, Never>] = []

    func acquire() async {
        guard isLocked else {
            isLocked = true
            return
        }
        await withCheckedContinuation { continuation in
            waiters.append(continuation)
        }
    }

    func release() {
        guard !waiters.isEmpty else {
            isLocked = false
            return
        }
        waiters.removeFirst().resume()
    }
}

/// Serializes bind, heartbeat, and unbind operations and persists enough state
/// to prevent one physical installation from silently crossing user accounts.
public actor NotificationInstallationLifecycle {
    public static let defaultHeartbeatInterval: TimeInterval = 24 * 60 * 60

    private let stateStore: any NotificationInstallationStateStore
    private let heartbeatInterval: TimeInterval
    private let now: @Sendable () -> Date
    private let operationLock = NotificationInstallationOperationLock()

    public init(
        stateStore: any NotificationInstallationStateStore,
        heartbeatInterval: TimeInterval = NotificationInstallationLifecycle
            .defaultHeartbeatInterval,
        now: @escaping @Sendable () -> Date = { Date() }
    ) {
        precondition(heartbeatInterval >= 0, "Heartbeat interval must not be negative")
        self.stateStore = stateStore
        self.heartbeatInterval = heartbeatInterval
        self.now = now
    }

    /// Registers a new or changed binding. For an unchanged binding, this is a
    /// persisted no-op until the heartbeat interval elapses, then sends one
    /// heartbeat instead of re-registering the provider target.
    @discardableResult
    public func bindUser(
        _ userID: String,
        request: NotificationRegistrationRequest,
        forceRegistration: Bool = false,
        using transport: any NotificationInstallationTransport
    ) async throws -> NotificationInstallationBinding {
        try await withExclusiveOperation {
            try await bindUserLocked(
                userID,
                request: request,
                forceRegistration: forceRegistration,
                using: transport
            )
        }
    }

    private func bindUserLocked(
        _ userID: String,
        request: NotificationRegistrationRequest,
        forceRegistration: Bool,
        using transport: any NotificationInstallationTransport
    ) async throws -> NotificationInstallationBinding {
        let existing = await stateStore.load()
        if let existing {
            guard existing.userID == userID else {
                throw NotificationInstallationLifecycleError.bindingBelongsToAnotherUser
            }
            guard existing.phase == .bound else {
                throw NotificationInstallationLifecycleError.pendingUnbind
            }
        }

        let replacementTarget = NotificationTarget(
            provider: request.provider,
            type: request.targetType,
            value: request.target
        )
        let replacementMetadata = request.metadata
        let timestamp = now()

        if let existing,
           !forceRegistration,
           existing.target == replacementTarget,
           existing.metadata == replacementMetadata {
            let cleaned = await cleanupStaleInstallations(
                in: existing,
                using: transport
            )
            guard heartbeatIsDue(for: cleaned, at: timestamp) else {
                return cleaned
            }

            let heartbeatRequest = NotificationHeartbeatRequest(
                permission: replacementMetadata.permission,
                timezone: replacementMetadata.timezone,
                enabled: replacementMetadata.enabled
            )
            do {
                _ = try await transport.heartbeat(
                    installationID: cleaned.installationID,
                    request: heartbeatRequest
                )
            } catch NotificationInstallationTransportError.installationNotFound {
                return try await bindUserLocked(
                    userID,
                    request: request,
                    forceRegistration: true,
                    using: transport
                )
            }
            let refreshed = replacing(
                cleaned,
                metadata: replacementMetadata,
                lastHeartbeatAt: timestamp
            )
            await stateStore.save(refreshed)
            return refreshed
        }

        let receipt = try await transport.register(request)
        var pendingCleanup = existing?.pendingCleanupInstallationIDs ?? []
        if let existing, existing.installationID != receipt.installationID {
            pendingCleanup.append(existing.installationID)
        }
        // A provider may rotate back to a previously-seen target while cleanup
        // of that target is still pending. The newly registered installation is
        // authoritative and must never be deleted as stale.
        pendingCleanup.removeAll { $0 == receipt.installationID }
        pendingCleanup = Array(Set(pendingCleanup)).sorted()

        let binding = NotificationInstallationBinding(
            userID: userID,
            installationID: receipt.installationID,
            target: replacementTarget,
            metadata: replacementMetadata,
            lastHeartbeatAt: timestamp,
            pendingCleanupInstallationIDs: pendingCleanup
        )
        await stateStore.save(binding)
        return await cleanupStaleInstallations(in: binding, using: transport)
    }

    @discardableResult
    public func heartbeat(
        for userID: String,
        request: NotificationHeartbeatRequest,
        force: Bool = false,
        using transport: any NotificationInstallationTransport
    ) async throws -> NotificationInstallationReceipt? {
        try await withExclusiveOperation {
            try await heartbeatLocked(
                for: userID,
                request: request,
                force: force,
                using: transport
            )
        }
    }

    private func heartbeatLocked(
        for userID: String,
        request: NotificationHeartbeatRequest,
        force: Bool,
        using transport: any NotificationInstallationTransport
    ) async throws -> NotificationInstallationReceipt? {
        guard let binding = await stateStore.load() else { return nil }
        guard binding.userID == userID else {
            throw NotificationInstallationLifecycleError.userMismatch
        }
        guard binding.phase == .bound else {
            throw NotificationInstallationLifecycleError.pendingUnbind
        }
        let cleaned = await cleanupStaleInstallations(in: binding, using: transport)
        guard let currentMetadata = cleaned.metadata else {
            // Missing metadata cannot be compared safely; bindUser must refresh it.
            return nil
        }

        let replacementMetadata = NotificationInstallationMetadata(
            platform: currentMetadata.platform,
            permission: request.permission ?? currentMetadata.permission,
            timezone: request.timezone ?? currentMetadata.timezone,
            enabled: request.enabled ?? currentMetadata.enabled
        )
        if replacementMetadata != currentMetadata {
            let registration = NotificationRegistrationRequest(
                target: cleaned.target,
                platform: replacementMetadata.platform,
                permission: replacementMetadata.permission,
                timezone: replacementMetadata.timezone,
                enabled: replacementMetadata.enabled
            )
            let rebound = try await bindUserLocked(
                userID,
                request: registration,
                forceRegistration: true,
                using: transport
            )
            return NotificationInstallationReceipt(
                installationID: rebound.installationID
            )
        }

        let timestamp = now()
        guard force || heartbeatIsDue(for: cleaned, at: timestamp) else {
            return NotificationInstallationReceipt(
                installationID: cleaned.installationID
            )
        }

        let receipt: NotificationInstallationReceipt
        do {
            receipt = try await transport.heartbeat(
                installationID: cleaned.installationID,
                request: request
            )
        } catch NotificationInstallationTransportError.installationNotFound {
            let registration = NotificationRegistrationRequest(
                target: cleaned.target,
                platform: replacementMetadata.platform,
                permission: replacementMetadata.permission,
                timezone: replacementMetadata.timezone,
                enabled: replacementMetadata.enabled
            )
            let rebound = try await bindUserLocked(
                userID,
                request: registration,
                forceRegistration: true,
                using: transport
            )
            return NotificationInstallationReceipt(
                installationID: rebound.installationID
            )
        }
        await stateStore.save(replacing(
            cleaned,
            metadata: replacementMetadata,
            lastHeartbeatAt: timestamp
        ))
        return receipt
    }

    /// Marks the binding pending before the network request. A crash or failed
    /// DELETE therefore blocks a later account from binding the same target.
    public func unbindUser(
        _ userID: String,
        using transport: any NotificationInstallationTransport
    ) async throws {
        try await withExclusiveOperation {
            try await unbindUserLocked(userID, using: transport)
        }
    }

    private func unbindUserLocked(
        _ userID: String,
        using transport: any NotificationInstallationTransport
    ) async throws {
        guard let binding = await stateStore.load() else { return }
        guard binding.userID == userID else {
            throw NotificationInstallationLifecycleError.userMismatch
        }

        let pending = NotificationInstallationBinding(
            userID: binding.userID,
            installationID: binding.installationID,
            target: binding.target,
            metadata: binding.metadata,
            lastHeartbeatAt: binding.lastHeartbeatAt,
            phase: .pendingUnbind,
            pendingCleanupInstallationIDs: binding.pendingCleanupInstallationIDs
        )
        await stateStore.save(pending)
        try await unregisterIfPresent(
            binding.installationID,
            using: transport
        )
        for staleID in binding.pendingCleanupInstallationIDs {
            try await unregisterIfPresent(staleID, using: transport)
        }
        await stateStore.clear()
    }

    /// Quarantines a binding when the app no longer has an authenticated
    /// transport with which to delete it. Provider registration must also be
    /// suspended before allowing authentication to change.
    public func markPendingUnbind(for userID: String) async throws {
        try await withExclusiveOperation {
            try await markPendingUnbindLocked(for: userID)
        }
    }

    private func markPendingUnbindLocked(for userID: String) async throws {
        guard let binding = await stateStore.load() else { return }
        guard binding.userID == userID else {
            throw NotificationInstallationLifecycleError.userMismatch
        }
        await stateStore.save(NotificationInstallationBinding(
            userID: binding.userID,
            installationID: binding.installationID,
            target: binding.target,
            metadata: binding.metadata,
            lastHeartbeatAt: binding.lastHeartbeatAt,
            phase: .pendingUnbind,
            pendingCleanupInstallationIDs: binding.pendingCleanupInstallationIDs
        ))
    }

    public func binding() async -> NotificationInstallationBinding? {
        try? await withExclusiveOperation {
            await stateStore.load()
        }
    }

    public func waitForIdle() async {
        await operationLock.acquire()
        await operationLock.release()
    }

    public func forgetDeletedAccountBinding(for userID: String) async throws {
        try await forgetBinding(for: userID)
    }

    public func forgetInvalidatedBinding(for userID: String) async throws {
        try await forgetBinding(for: userID)
    }

    private func forgetBinding(for userID: String) async throws {
        try await withExclusiveOperation {
            guard let binding = await stateStore.load() else { return }
            guard binding.userID == userID else {
                throw NotificationInstallationLifecycleError.userMismatch
            }
            await stateStore.clear()
        }
    }

    private func withExclusiveOperation<T: Sendable>(
        _ operation: () async throws -> T
    ) async throws -> T {
        await operationLock.acquire()
        do {
            try Task.checkCancellation()
            let result = try await operation()
            await operationLock.release()
            return result
        } catch {
            await operationLock.release()
            throw error
        }
    }

    /// The replacement remains active throughout cleanup. Failed deletes stay
    /// persisted and are retried on the next bind or heartbeat.
    private func cleanupStaleInstallations(
        in binding: NotificationInstallationBinding,
        using transport: any NotificationInstallationTransport
    ) async -> NotificationInstallationBinding {
        var remaining: [String] = []
        for installationID in binding.pendingCleanupInstallationIDs {
            do {
                try await unregisterIfPresent(installationID, using: transport)
            } catch {
                remaining.append(installationID)
            }
        }
        guard remaining != binding.pendingCleanupInstallationIDs else {
            return binding
        }

        let cleaned = NotificationInstallationBinding(
            userID: binding.userID,
            installationID: binding.installationID,
            target: binding.target,
            metadata: binding.metadata,
            lastHeartbeatAt: binding.lastHeartbeatAt,
            phase: binding.phase,
            pendingCleanupInstallationIDs: remaining
        )
        await stateStore.save(cleaned)
        return cleaned
    }

    private func unregisterIfPresent(
        _ installationID: String,
        using transport: any NotificationInstallationTransport
    ) async throws {
        do {
            try await transport.unregister(installationID: installationID)
        } catch NotificationInstallationTransportError.installationNotFound {
        }
    }

    private func heartbeatIsDue(
        for binding: NotificationInstallationBinding,
        at timestamp: Date
    ) -> Bool {
        guard let lastHeartbeatAt = binding.lastHeartbeatAt else { return true }
        return timestamp.timeIntervalSince(lastHeartbeatAt) >= heartbeatInterval
    }

    private func replacing(
        _ binding: NotificationInstallationBinding,
        metadata: NotificationInstallationMetadata,
        lastHeartbeatAt: Date
    ) -> NotificationInstallationBinding {
        NotificationInstallationBinding(
            userID: binding.userID,
            installationID: binding.installationID,
            target: binding.target,
            metadata: metadata,
            lastHeartbeatAt: lastHeartbeatAt,
            phase: binding.phase,
            pendingCleanupInstallationIDs: binding.pendingCleanupInstallationIDs
        )
    }
}
