import Foundation

public struct NotificationPayload: Codable, Equatable, Sendable {
    public let notificationID: String?
    public let schemaVersion: String
    public let app: String?
    public let kind: String?
    public let contentID: String?
    public let deepLinkURL: URL?
    public let data: [String: String]

    public init(
        notificationID: String? = nil,
        schemaVersion: String = "1",
        app: String? = nil,
        kind: String? = nil,
        contentID: String? = nil,
        deepLinkURL: URL? = nil,
        data: [String: String] = [:]
    ) {
        self.notificationID = notificationID
        self.schemaVersion = schemaVersion
        self.app = app
        self.kind = kind
        self.contentID = contentID
        self.deepLinkURL = deepLinkURL
        self.data = data
    }

    /// Accepts both snake-case and camel-case payload keys.
    public init(userInfo: [AnyHashable: Any]) {
        let strings = userInfo.reduce(into: [String: String]()) { result, entry in
            guard let key = entry.key as? String else { return }
            switch entry.value {
            case let value as String:
                result[key] = value
            case let value as NSNumber:
                result[key] = value.stringValue
            default:
                break
            }
        }

        notificationID = Self.firstValue(
            for: ["notification_id", "notificationId", "gcm.message_id"],
            in: strings
        )
        schemaVersion = Self.firstValue(
            for: ["schema_version", "schemaVersion"],
            in: strings
        ) ?? "1"
        app = strings["app"]
        kind = Self.firstValue(for: ["kind", "type"], in: strings)
        contentID = Self.firstValue(for: ["content_id", "contentId"], in: strings)
        deepLinkURL = Self.firstValue(
            for: ["deep_link", "deepLink"],
            in: strings
        ).flatMap(URL.init(string:))
        data = strings
    }

    private static func firstValue(
        for keys: [String],
        in values: [String: String]
    ) -> String? {
        keys.lazy.compactMap { key in
            values[key].flatMap { $0.isEmpty ? nil : $0 }
        }.first
    }
}

public enum NotificationPresentation: String, Codable, Sendable {
    case app
    case system
}

public struct NotificationOpenEvent: Equatable, Sendable {
    public let payload: NotificationPayload
    public let provider: NotificationProvider
    public let presentation: NotificationPresentation
    public let actionIdentifier: String?
    public let openedAt: Date

    public init(
        payload: NotificationPayload,
        provider: NotificationProvider,
        presentation: NotificationPresentation,
        actionIdentifier: String? = nil,
        openedAt: Date = Date()
    ) {
        self.payload = payload
        self.provider = provider
        self.presentation = presentation
        self.actionIdentifier = actionIdentifier
        self.openedAt = openedAt
    }
}

public enum NotificationAnalytics {
    public static let openEvent = "push_notification_open"

    public static func openParameters(for event: NotificationOpenEvent) -> [String: String]? {
        let payload = event.payload
        guard let notificationID = payload.data["notification_id"],
              !notificationID.isEmpty else { return nil }
        var parameters = [
            "notification_id": notificationID,
            "schema_version": payload.schemaVersion,
            "presentation": event.presentation.rawValue,
            "provider": event.provider.rawValue,
        ]
        parameters["notification_app"] = payload.app
        parameters["notification_kind"] = payload.kind
        parameters["analytics_label"] = payload.data["analytics_label"]
        return parameters
    }
}

/// Main-actor delivery keeps navigation hooks safe for SwiftUI/UIKit consumers.
@MainActor
public protocol NotificationEventDelegate: AnyObject {
    func notificationRegistrationDidUpdate(_ target: NotificationTarget)
    func notificationRegistrationDidInvalidate(_ target: NotificationTarget)
    func notificationDidReceive(_ payload: NotificationPayload)
    func notificationDidOpen(_ event: NotificationOpenEvent)
}

public extension NotificationEventDelegate {
    func notificationRegistrationDidInvalidate(_ target: NotificationTarget) {}
    func notificationDidReceive(_ payload: NotificationPayload) {}
    func notificationDidOpen(_ event: NotificationOpenEvent) {}
}
