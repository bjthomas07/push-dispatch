import XCTest
@testable import PushDispatchCore

final class NotificationPayloadTests: XCTestCase {
    func testParsesVersionedSnakeCasePayload() {
        let payload = NotificationPayload(userInfo: [
            "notification_id": "notification-1",
            "schema_version": "2",
            "app": "exampleapp",
            "kind": "daily_reminder",
            "content_id": "item-123",
            "deep_link": "exampleapp://content/item-123",
        ])

        XCTAssertEqual(payload.notificationID, "notification-1")
        XCTAssertEqual(payload.schemaVersion, "2")
        XCTAssertEqual(payload.app, "exampleapp")
        XCTAssertEqual(payload.kind, "daily_reminder")
        XCTAssertEqual(payload.contentID, "item-123")
        XCTAssertEqual(payload.deepLinkURL?.absoluteString, "exampleapp://content/item-123")
    }

    func testParsesLegacyCamelCasePayload() {
        let payload = NotificationPayload(userInfo: [
            "gcm.message_id": "message-1",
            "type": "dailyReading",
            "deepLink": "exampleapp://daily_reading",
            "badge": NSNumber(value: 1),
        ])

        XCTAssertEqual(payload.notificationID, "message-1")
        XCTAssertEqual(payload.schemaVersion, "1")
        XCTAssertEqual(payload.kind, "dailyReading")
        XCTAssertEqual(payload.deepLinkURL?.absoluteString, "exampleapp://daily_reading")
        XCTAssertEqual(payload.data["badge"], "1")
    }

    func testInvalidDeepLinkDoesNotProduceURL() {
        let payload = NotificationPayload(userInfo: ["deep_link": ":// invalid"])

        XCTAssertNil(payload.deepLinkURL)
    }

    func testBuildsPrivacySafeOpenAnalyticsParameters() {
        let payload = NotificationPayload(userInfo: [
            "notification_id": "pl_morning_2026-08-29_1200",
            "schema_version": "1",
            "app": "exampleapp",
            "kind": "morning",
            "analytics_label": "pl_morning_2026-08-29",
            "gcm.message_id": "provider-message",
            "deepLink": "exampleapp://today",
        ])

        let event = NotificationOpenEvent(
            payload: payload,
            provider: .fcm,
            presentation: .system
        )

        XCTAssertEqual(NotificationAnalytics.openParameters(for: event), [
            "notification_id": "pl_morning_2026-08-29_1200",
            "schema_version": "1",
            "notification_app": "exampleapp",
            "notification_kind": "morning",
            "analytics_label": "pl_morning_2026-08-29",
            "presentation": "system",
            "provider": "fcm",
        ])
    }

    func testOpenAnalyticsRequiresAnExplicitLogicalNotificationIdentity() {
        let legacyPayload = NotificationPayload(userInfo: [
            "gcm.message_id": "provider-message",
            "deepLink": "exampleapp://today",
        ])
        let event = NotificationOpenEvent(
            payload: legacyPayload,
            provider: .fcm,
            presentation: .system
        )

        XCTAssertEqual(legacyPayload.notificationID, "provider-message")
        XCTAssertNil(NotificationAnalytics.openParameters(for: event))
    }
}
