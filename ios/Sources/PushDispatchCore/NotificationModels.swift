import Foundation

public enum NotificationProvider: String, Codable, Sendable {
    case fcm
}

public enum NotificationTargetType: String, Codable, Sendable {
    case firebaseInstallationID = "fid"
    case registrationToken = "token"
}

public enum NotificationPlatform: String, Codable, Sendable {
    case iOS = "ios"
    case android
}

/// Backend-facing permission state. Provisional and ephemeral iOS permission
/// both map to `granted`; unsupported/future states map to `unknown`.
public enum NotificationPermission: String, Codable, Sendable {
    case granted
    case denied
    case unknown
}

public struct NotificationTarget: Codable, Equatable, Sendable {
    public let provider: NotificationProvider
    public let type: NotificationTargetType
    public let value: String

    public init(
        provider: NotificationProvider,
        type: NotificationTargetType,
        value: String
    ) {
        self.provider = provider
        self.type = type
        self.value = value
    }
}

public struct NotificationInstallationMetadata: Codable, Equatable, Sendable {
    public let platform: NotificationPlatform
    public let permission: NotificationPermission
    public let timezone: String
    public let enabled: Bool

    public init(
        platform: NotificationPlatform,
        permission: NotificationPermission,
        timezone: String,
        enabled: Bool = true
    ) {
        self.platform = platform
        self.permission = permission
        self.timezone = timezone
        self.enabled = enabled
    }
}

/// Authentication identifies the user; callers cannot supply a UID in the body.
public struct NotificationRegistrationRequest: Codable, Equatable, Sendable {
    public let provider: NotificationProvider
    public let targetType: NotificationTargetType
    public let target: String
    public let platform: NotificationPlatform
    public let permission: NotificationPermission
    public let timezone: String
    public let enabled: Bool

    public init(
        target: NotificationTarget,
        platform: NotificationPlatform,
        permission: NotificationPermission,
        timezone: String,
        enabled: Bool = true
    ) {
        self.provider = target.provider
        self.targetType = target.type
        self.target = target.value
        self.platform = platform
        self.permission = permission
        self.timezone = timezone
        self.enabled = enabled
    }

    public var metadata: NotificationInstallationMetadata {
        NotificationInstallationMetadata(
            platform: platform,
            permission: permission,
            timezone: timezone,
            enabled: enabled
        )
    }
}

public struct NotificationHeartbeatRequest: Codable, Equatable, Sendable {
    public let permission: NotificationPermission?
    public let timezone: String?
    public let enabled: Bool?

    public init(
        permission: NotificationPermission? = nil,
        timezone: String? = nil,
        enabled: Bool? = nil
    ) {
        self.permission = permission
        self.timezone = timezone
        self.enabled = enabled
    }
}

public struct NotificationInstallationReceipt: Codable, Equatable, Sendable {
    public let installationID: String

    public init(installationID: String) {
        self.installationID = installationID
    }

    private enum CodingKeys: String, CodingKey {
        case installationID = "installationId"
    }
}

public enum NotificationInstallationTransportError: Error, Equatable, Sendable {
    case installationNotFound
}

/// App-owned authenticated transport for installation lifecycle requests.
public protocol NotificationInstallationTransport: Sendable {
    func register(
        _ request: NotificationRegistrationRequest
    ) async throws -> NotificationInstallationReceipt

    func heartbeat(
        installationID: String,
        request: NotificationHeartbeatRequest
    ) async throws -> NotificationInstallationReceipt

    func unregister(installationID: String) async throws
}
