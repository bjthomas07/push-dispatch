import XCTest
@testable import PushDispatchCore

final class NotificationModelsTests: XCTestCase {
    func testRegistrationRequestUsesBackendContractKeysAndValues() throws {
        let request = NotificationRegistrationRequest(
            target: NotificationTarget(
                provider: .fcm,
                type: .firebaseInstallationID,
                value: "fid-123"
            ),
            platform: .iOS,
            permission: .granted,
            timezone: "America/New_York"
        )

        let data = try JSONEncoder().encode(request)
        let json = try XCTUnwrap(
            JSONSerialization.jsonObject(with: data) as? [String: Any]
        )

        XCTAssertEqual(json["provider"] as? String, "fcm")
        XCTAssertEqual(json["targetType"] as? String, "fid")
        XCTAssertEqual(json["target"] as? String, "fid-123")
        XCTAssertEqual(json["platform"] as? String, "ios")
        XCTAssertEqual(json["permission"] as? String, "granted")
        XCTAssertEqual(json["timezone"] as? String, "America/New_York")
        XCTAssertEqual(json["enabled"] as? Bool, true)
    }

    func testReceiptDecodesInstallationIdContract() throws {
        let data = Data(#"{"installationId":"sha256-installation"}"#.utf8)
        let receipt = try JSONDecoder().decode(
            NotificationInstallationReceipt.self,
            from: data
        )

        XCTAssertEqual(receipt.installationID, "sha256-installation")
    }

    func testHeartbeatEncodesOnlySpecifiedFields() throws {
        let heartbeat = NotificationHeartbeatRequest(
            permission: .denied,
            timezone: "UTC",
            enabled: false
        )

        let data = try JSONEncoder().encode(heartbeat)
        let json = try XCTUnwrap(
            JSONSerialization.jsonObject(with: data) as? [String: Any]
        )

        XCTAssertEqual(json["permission"] as? String, "denied")
        XCTAssertEqual(json["timezone"] as? String, "UTC")
        XCTAssertEqual(json["enabled"] as? Bool, false)
    }

    func testBindingDecodesStateWrittenBeforeThrottleMetadata() throws {
        let data = Data(
            #"{"userID":"user-1","installationID":"installation-1","target":{"provider":"fcm","type":"fid","value":"fid-1"},"phase":"bound","pendingCleanupInstallationIDs":[]}"#.utf8
        )

        let binding = try JSONDecoder().decode(
            NotificationInstallationBinding.self,
            from: data
        )

        XCTAssertEqual(binding.userID, "user-1")
        XCTAssertNil(binding.metadata)
        XCTAssertNil(binding.lastHeartbeatAt)
    }
}
