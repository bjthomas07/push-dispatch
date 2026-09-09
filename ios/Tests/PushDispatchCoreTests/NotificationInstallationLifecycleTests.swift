import XCTest
@testable import PushDispatchCore

final class NotificationInstallationLifecycleTests: XCTestCase {
    func testBindThenHeartbeatUsesServerInstallationID() async throws {
        let store = MemoryStateStore()
        let transport = RecordingTransport()
        let lifecycle = NotificationInstallationLifecycle(
            stateStore: store,
            heartbeatInterval: 0
        )

        let binding = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )
        let receipt = try await lifecycle.heartbeat(
            for: "user-1",
            request: NotificationHeartbeatRequest(permission: .granted),
            using: transport
        )

        XCTAssertEqual(binding.installationID, "installation-1")
        XCTAssertEqual(receipt?.installationID, "installation-1")
        let events = await transport.events
        XCTAssertEqual(events, [.register, .heartbeat("installation-1")])
    }

    func testRepeatedBindWithinIntervalDoesNotCallBackendAgain() async throws {
        let clock = MutableDateClock(Date(timeIntervalSince1970: 1_000))
        let store = MemoryStateStore()
        let transport = RecordingTransport()
        let lifecycle = NotificationInstallationLifecycle(
            stateStore: store,
            heartbeatInterval: 100,
            now: { clock.now }
        )

        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )
        clock.advance(by: 99)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )

        let events = await transport.events
        XCTAssertEqual(events, [.register])
    }

    func testHeartbeatThrottlePersistsAcrossLifecycleInstances() async throws {
        let clock = MutableDateClock(Date(timeIntervalSince1970: 1_000))
        let store = MemoryStateStore()
        let transport = RecordingTransport()
        var lifecycle = NotificationInstallationLifecycle(
            stateStore: store,
            heartbeatInterval: 100,
            now: { clock.now }
        )

        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )
        clock.advance(by: 101)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )

        lifecycle = NotificationInstallationLifecycle(
            stateStore: store,
            heartbeatInterval: 100,
            now: { clock.now }
        )
        clock.advance(by: 99)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )
        clock.advance(by: 2)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )

        let events = await transport.events
        XCTAssertEqual(
            events,
            [
                .register,
                .heartbeat("installation-1"),
                .heartbeat("installation-1"),
            ]
        )
        let binding = await lifecycle.binding()
        XCTAssertEqual(binding?.lastHeartbeatAt, clock.now)
    }

    func testDefaultHeartbeatRunsAtTwentyFourHours() async throws {
        let clock = MutableDateClock(Date(timeIntervalSince1970: 1_000))
        let transport = RecordingTransport()
        let lifecycle = NotificationInstallationLifecycle(
            stateStore: MemoryStateStore(),
            now: { clock.now }
        )

        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )
        clock.advance(
            by: NotificationInstallationLifecycle.defaultHeartbeatInterval - 1
        )
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )
        clock.advance(by: 1)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )

        let events = await transport.events
        XCTAssertEqual(events, [.register, .heartbeat("installation-1")])
    }

    func testMissingInstallationHeartbeatReregistersSameTarget() async throws {
        let transport = RecordingTransport(
            registrationIDs: ["installation-old", "installation-new"],
            heartbeatNotFoundFailures: 1
        )
        let lifecycle = NotificationInstallationLifecycle(
            stateStore: MemoryStateStore(),
            heartbeatInterval: 0
        )

        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )
        let recovered = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )

        XCTAssertEqual(recovered.target, Self.registrationRequestTarget)
        XCTAssertEqual(recovered.installationID, "installation-new")
        XCTAssertTrue(recovered.pendingCleanupInstallationIDs.isEmpty)
        let events = await transport.events
        XCTAssertEqual(
            events,
            [
                .register,
                .heartbeat("installation-old"),
                .register,
                .unregister("installation-old"),
            ]
        )
    }

    func testExplicitMissingHeartbeatReregistersSameTarget() async throws {
        let transport = RecordingTransport(
            registrationIDs: ["installation-old", "installation-new"],
            heartbeatNotFoundFailures: 1
        )
        let lifecycle = NotificationInstallationLifecycle(
            stateStore: MemoryStateStore()
        )
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )

        let receipt = try await lifecycle.heartbeat(
            for: "user-1",
            request: NotificationHeartbeatRequest(permission: .granted),
            force: true,
            using: transport
        )

        XCTAssertEqual(receipt?.installationID, "installation-new")
        let binding = await lifecycle.binding()
        XCTAssertEqual(binding?.target, Self.registrationRequestTarget)
        XCTAssertEqual(binding?.installationID, "installation-new")
    }

    func testSameTargetColdStartCallbackHonorsPersistedThrottle() async throws {
        let clock = MutableDateClock(Date(timeIntervalSince1970: 1_000))
        let store = MemoryStateStore()
        let transport = RecordingTransport()
        var lifecycle = NotificationInstallationLifecycle(
            stateStore: store,
            now: { clock.now }
        )

        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )

        // Firebase delivers the existing FID again once per process start. A
        // newly-created lifecycle represents that cold start and must reuse the
        // persisted throttle rather than POSTing the unchanged target again.
        lifecycle = NotificationInstallationLifecycle(
            stateStore: store,
            now: { clock.now }
        )
        clock.advance(by: 23 * 60 * 60)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )

        let events = await transport.events
        XCTAssertEqual(events, [.register])
    }

    func testMetadataChangesRegisterImmediately() async throws {
        let clock = MutableDateClock(Date(timeIntervalSince1970: 1_000))
        let store = MemoryStateStore()
        let transport = RecordingTransport()
        let lifecycle = NotificationInstallationLifecycle(
            stateStore: store,
            now: { clock.now }
        )

        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest(permission: .denied),
            using: transport
        )
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest(
                permission: .denied,
                timezone: "America/New_York"
            ),
            using: transport
        )
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest(
                permission: .denied,
                timezone: "America/New_York",
                enabled: false
            ),
            using: transport
        )
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest(
                platform: .android,
                permission: .denied,
                timezone: "America/New_York",
                enabled: false
            ),
            using: transport
        )

        let events = await transport.events
        XCTAssertEqual(
            events,
            [.register, .register, .register, .register, .register]
        )
        let binding = await lifecycle.binding()
        XCTAssertEqual(binding?.metadata?.permission, .denied)
        XCTAssertEqual(binding?.metadata?.timezone, "America/New_York")
        XCTAssertEqual(binding?.metadata?.enabled, false)
        XCTAssertEqual(binding?.metadata?.platform, .android)
    }

    func testForcedRegistrationBypassesUnchangedBindingThrottle() async throws {
        let store = MemoryStateStore()
        let transport = RecordingTransport()
        let lifecycle = NotificationInstallationLifecycle(stateStore: store)

        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            forceRegistration: true,
            using: transport
        )

        let events = await transport.events
        XCTAssertEqual(events, [.register, .register])
    }

    func testLegacyBindingWithoutMetadataRegistersOnceToUpgradeState() async throws {
        let store = MemoryStateStore(state: NotificationInstallationBinding(
            userID: "user-1",
            installationID: "installation-1",
            target: Self.registrationRequestTarget
        ))
        let transport = RecordingTransport()
        let lifecycle = NotificationInstallationLifecycle(stateStore: store)

        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )

        let events = await transport.events
        XCTAssertEqual(events, [.register])
        let binding = await lifecycle.binding()
        XCTAssertEqual(binding?.metadata, Self.registrationRequest.metadata)
        XCTAssertNotNil(binding?.lastHeartbeatAt)
    }

    func testNewUserRegistersAfterPriorUserIsUnbound() async throws {
        let store = MemoryStateStore()
        let transport = RecordingTransport(
            registrationIDs: ["installation-1", "installation-2"]
        )
        let lifecycle = NotificationInstallationLifecycle(stateStore: store)

        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )
        try await lifecycle.unbindUser("user-1", using: transport)
        let rebound = try await lifecycle.bindUser(
            "user-2",
            request: Self.registrationRequest,
            using: transport
        )

        let events = await transport.events
        XCTAssertEqual(
            events,
            [.register, .unregister("installation-1"), .register]
        )
        XCTAssertEqual(rebound.userID, "user-2")
        XCTAssertEqual(rebound.installationID, "installation-2")
    }

    func testUnbindPersistsPendingBeforeTransportDeleteThenClears() async throws {
        let recorder = EventRecorder()
        let store = MemoryStateStore(recorder: recorder)
        let transport = RecordingTransport(recorder: recorder)
        let lifecycle = NotificationInstallationLifecycle(stateStore: store)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )
        await recorder.reset()

        try await lifecycle.unbindUser("user-1", using: transport)

        let events = await recorder.events
        XCTAssertEqual(events, [.savedPendingUnbind, .unregister, .cleared])
        let binding = await lifecycle.binding()
        XCTAssertNil(binding)
    }

    func testFailedUnbindRemainsPendingAndBlocksDifferentUser() async throws {
        let store = MemoryStateStore()
        let transport = RecordingTransport(failUnregister: true)
        let lifecycle = NotificationInstallationLifecycle(stateStore: store)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )

        do {
            try await lifecycle.unbindUser("user-1", using: transport)
            XCTFail("Expected unregister failure")
        } catch RecordingTransport.TestError.unregisterFailed {
        } catch {
            XCTFail("Unexpected error: \(error)")
        }

        let pending = await lifecycle.binding()
        XCTAssertEqual(pending?.phase, .pendingUnbind)

        do {
            _ = try await lifecycle.bindUser(
                "user-2",
                request: Self.registrationRequest,
                using: transport
            )
            XCTFail("A second user must not bind before the first unbind completes")
        } catch let error as NotificationInstallationLifecycleError {
            XCTAssertEqual(error, .bindingBelongsToAnotherUser)
        } catch {
            XCTFail("Unexpected error: \(error)")
        }

        let events = await transport.events
        XCTAssertEqual(events.filter { $0 == .register }.count, 1)
    }

    func testPendingUnbindRetriesWhileOwningUserIsStillAuthenticated() async throws {
        let store = MemoryStateStore()
        let transport = RecordingTransport(
            failingUnregisterIDs: ["installation-1"]
        )
        let lifecycle = NotificationInstallationLifecycle(stateStore: store)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )

        do {
            try await lifecycle.unbindUser("user-1", using: transport)
            XCTFail("Expected first DELETE to fail")
        } catch RecordingTransport.TestError.unregisterFailed {}

        await transport.allowUnregister("installation-1")
        try await lifecycle.unbindUser("user-1", using: transport)

        let binding = await lifecycle.binding()
        XCTAssertNil(binding)
        let events = await transport.events
        XCTAssertEqual(
            events.filter { $0 == .unregister("installation-1") }.count,
            2
        )
    }

    func testTargetRotationRegistersNewBeforeUnregisteringOld() async throws {
        let store = MemoryStateStore()
        let transport = RecordingTransport(
            registrationIDs: ["installation-old", "installation-new"]
        )
        let lifecycle = NotificationInstallationLifecycle(stateStore: store)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )

        let replacement = NotificationRegistrationRequest(
            target: NotificationTarget(
                provider: .fcm,
                type: .firebaseInstallationID,
                value: "fid-2"
            ),
            platform: .iOS,
            permission: .granted,
            timezone: "UTC"
        )
        let binding = try await lifecycle.bindUser(
            "user-1",
            request: replacement,
            using: transport
        )

        let events = await transport.events
        XCTAssertEqual(
            events,
            [.register, .register, .unregister("installation-old")]
        )
        XCTAssertEqual(binding.installationID, "installation-new")
        XCTAssertTrue(binding.pendingCleanupInstallationIDs.isEmpty)
    }

    func testFailedRotationCleanupIsPersistedAndRetriedBeforeHeartbeat() async throws {
        let store = MemoryStateStore()
        let transport = RecordingTransport(
            registrationIDs: ["installation-old", "installation-new"],
            failingUnregisterIDs: ["installation-old"]
        )
        let lifecycle = NotificationInstallationLifecycle(stateStore: store)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )
        _ = try await lifecycle.bindUser(
            "user-1",
            request: NotificationRegistrationRequest(
                target: NotificationTarget(
                    provider: .fcm,
                    type: .firebaseInstallationID,
                    value: "fid-2"
                ),
                platform: .iOS,
                permission: .granted,
                timezone: "UTC"
            ),
            using: transport
        )

        var binding = await lifecycle.binding()
        XCTAssertEqual(
            binding?.pendingCleanupInstallationIDs,
            ["installation-old"]
        )

        await transport.allowUnregister("installation-old")
        _ = try await lifecycle.heartbeat(
            for: "user-1",
            request: NotificationHeartbeatRequest(permission: .granted),
            force: true,
            using: transport
        )

        binding = await lifecycle.binding()
        XCTAssertTrue(binding?.pendingCleanupInstallationIDs.isEmpty == true)
        let events = await transport.events
        XCTAssertEqual(
            Array(events.suffix(2)),
            [.unregister("installation-old"), .heartbeat("installation-new")]
        )
    }

    func testRotationBackNeverDeletesNewlyReactivatedInstallation() async throws {
        let store = MemoryStateStore()
        let transport = RecordingTransport(
            registrationIDs: [
                "installation-a",
                "installation-b",
                "installation-a",
            ],
            failingUnregisterIDs: ["installation-a"]
        )
        let lifecycle = NotificationInstallationLifecycle(stateStore: store)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest(targetValue: "fid-2"),
            using: transport
        )

        let reactivated = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: transport
        )

        XCTAssertEqual(reactivated.installationID, "installation-a")
        XCTAssertTrue(reactivated.pendingCleanupInstallationIDs.isEmpty)
        let events = await transport.events
        XCTAssertEqual(
            events.filter { $0 == .unregister("installation-a") }.count,
            1,
            "The only A cleanup is the failed attempt before A becomes active again"
        )
        XCTAssertEqual(
            Array(events.suffix(2)),
            [.register, .unregister("installation-b")]
        )
    }

    func testConcurrentRotationsCommitInCallOrder() async throws {
        let store = MemoryStateStore()
        let lifecycle = NotificationInstallationLifecycle(stateStore: store)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: RecordingTransport(registrationIDs: ["installation-1"])
        )

        let transport = BlockingRegistrationTransport(
            registrationIDs: ["installation-2", "installation-3"]
        )
        let first = Task {
            try await lifecycle.bindUser(
                "user-1",
                request: Self.registrationRequest(targetValue: "fid-2"),
                using: transport
            )
        }
        await transport.waitUntilFirstRegistrationStarts()
        let second = Task {
            try await lifecycle.bindUser(
                "user-1",
                request: Self.registrationRequest(targetValue: "fid-3"),
                using: transport
            )
        }
        await Task.yield()

        let registrationCountBeforeRelease = await transport.registrationCount
        XCTAssertEqual(registrationCountBeforeRelease, 1)
        await transport.releaseFirstRegistration()
        _ = try await first.value
        _ = try await second.value

        let binding = await lifecycle.binding()
        XCTAssertEqual(binding?.target.value, "fid-3")
        XCTAssertEqual(binding?.installationID, "installation-3")
    }

    func testInFlightBindFinishesBeforeQueuedUnbind() async throws {
        let store = MemoryStateStore()
        let lifecycle = NotificationInstallationLifecycle(stateStore: store)
        _ = try await lifecycle.bindUser(
            "user-1",
            request: Self.registrationRequest,
            using: RecordingTransport(registrationIDs: ["installation-1"])
        )

        let transport = BlockingRegistrationTransport(
            registrationIDs: ["installation-2"]
        )
        let bind = Task {
            try await lifecycle.bindUser(
                "user-1",
                request: Self.registrationRequest(targetValue: "fid-2"),
                using: transport
            )
        }
        await transport.waitUntilFirstRegistrationStarts()
        let unbind = Task {
            try await lifecycle.unbindUser("user-1", using: transport)
        }
        await Task.yield()

        await transport.releaseFirstRegistration()
        _ = try await bind.value
        try await unbind.value

        let binding = await lifecycle.binding()
        XCTAssertNil(binding)
        let unregisteredInstallationIDs = await transport.unregisteredInstallationIDs
        XCTAssertEqual(
            unregisteredInstallationIDs,
            ["installation-1", "installation-2"]
        )
    }

    func testIdleBarrierWaitsForInFlightBind() async throws {
        let lifecycle = NotificationInstallationLifecycle(
            stateStore: MemoryStateStore()
        )
        let transport = BlockingRegistrationTransport(
            registrationIDs: ["installation-1"]
        )
        let bind = Task {
            try await lifecycle.bindUser(
                "user-1",
                request: Self.registrationRequest,
                using: transport
            )
        }
        await transport.waitUntilFirstRegistrationStarts()

        let barrierStarted = TestSignal()
        let barrierFinished = TestSignal()
        let barrier = Task {
            await barrierStarted.signal()
            await lifecycle.waitForIdle()
            await barrierFinished.signal()
        }
        await barrierStarted.wait()
        await Task.yield()

        let finishedWhileBindWasBlocked = await barrierFinished.isSignaled
        XCTAssertFalse(finishedWhileBindWasBlocked)

        await transport.releaseFirstRegistration()
        _ = try await bind.value
        await barrier.value
        let finishedAfterBind = await barrierFinished.isSignaled
        XCTAssertTrue(finishedAfterBind)
    }

    func testDeletedAccountCleanupIsOwnerScoped() async throws {
        let store = MemoryStateStore(state: NotificationInstallationBinding(
            userID: "user-1",
            installationID: "installation-1",
            target: Self.registrationRequestTarget
        ))
        let lifecycle = NotificationInstallationLifecycle(stateStore: store)

        do {
            try await lifecycle.forgetDeletedAccountBinding(for: "user-2")
            XCTFail("A different user must not clear the binding")
        } catch let error as NotificationInstallationLifecycleError {
            XCTAssertEqual(error, .userMismatch)
        }
        var binding = await lifecycle.binding()
        XCTAssertEqual(binding?.userID, "user-1")

        try await lifecycle.forgetDeletedAccountBinding(for: "user-1")
        binding = await lifecycle.binding()
        XCTAssertNil(binding)
    }

    private static let registrationRequestTarget = NotificationTarget(
        provider: .fcm,
        type: .firebaseInstallationID,
        value: "fid-1"
    )

    private static let registrationRequest = NotificationRegistrationRequest(
        target: registrationRequestTarget,
        platform: .iOS,
        permission: .granted,
        timezone: "UTC"
    )

    private static func registrationRequest(
        targetValue: String = "fid-1",
        platform: NotificationPlatform = .iOS,
        permission: NotificationPermission = .granted,
        timezone: String = "UTC",
        enabled: Bool = true
    ) -> NotificationRegistrationRequest {
        NotificationRegistrationRequest(
            target: NotificationTarget(
                provider: .fcm,
                type: .firebaseInstallationID,
                value: targetValue
            ),
            platform: platform,
            permission: permission,
            timezone: timezone,
            enabled: enabled
        )
    }
}

private final class MutableDateClock: @unchecked Sendable {
    private let lock = NSLock()
    private var value: Date

    init(_ value: Date) {
        self.value = value
    }

    var now: Date {
        lock.withLock { value }
    }

    func advance(by interval: TimeInterval) {
        lock.withLock {
            value = value.addingTimeInterval(interval)
        }
    }
}

private actor MemoryStateStore: NotificationInstallationStateStore {
    private var state: NotificationInstallationBinding?
    private let recorder: EventRecorder?

    init(
        state: NotificationInstallationBinding? = nil,
        recorder: EventRecorder? = nil
    ) {
        self.state = state
        self.recorder = recorder
    }

    func load() -> NotificationInstallationBinding? {
        state
    }

    func save(_ binding: NotificationInstallationBinding) async {
        state = binding
        if binding.phase == .pendingUnbind {
            await recorder?.record(.savedPendingUnbind)
        }
    }

    func clear() async {
        state = nil
        await recorder?.record(.cleared)
    }
}

private actor RecordingTransport: NotificationInstallationTransport {
    enum TestError: Error {
        case unregisterFailed
    }

    enum Event: Equatable {
        case register
        case heartbeat(String)
        case unregister(String)
    }

    private(set) var events: [Event] = []
    private let failUnregister: Bool
    private let recorder: EventRecorder?
    private var registrationIDs: [String]
    private var failingUnregisterIDs: Set<String>
    private var heartbeatNotFoundFailures: Int

    init(
        failUnregister: Bool = false,
        recorder: EventRecorder? = nil,
        registrationIDs: [String] = ["installation-1"],
        failingUnregisterIDs: Set<String> = [],
        heartbeatNotFoundFailures: Int = 0
    ) {
        self.failUnregister = failUnregister
        self.recorder = recorder
        self.registrationIDs = registrationIDs
        self.failingUnregisterIDs = failingUnregisterIDs
        self.heartbeatNotFoundFailures = heartbeatNotFoundFailures
    }

    func register(
        _ request: NotificationRegistrationRequest
    ) -> NotificationInstallationReceipt {
        events.append(.register)
        let installationID: String
        if registrationIDs.count > 1 {
            installationID = registrationIDs.removeFirst()
        } else {
            installationID = registrationIDs.first ?? "installation-1"
        }
        return NotificationInstallationReceipt(installationID: installationID)
    }

    func heartbeat(
        installationID: String,
        request: NotificationHeartbeatRequest
    ) throws -> NotificationInstallationReceipt {
        events.append(.heartbeat(installationID))
        if heartbeatNotFoundFailures > 0 {
            heartbeatNotFoundFailures -= 1
            throw NotificationInstallationTransportError.installationNotFound
        }
        return NotificationInstallationReceipt(installationID: installationID)
    }

    func unregister(installationID: String) async throws {
        events.append(.unregister(installationID))
        await recorder?.record(.unregister)
        if failUnregister || failingUnregisterIDs.contains(installationID) {
            throw TestError.unregisterFailed
        }
    }

    func allowUnregister(_ installationID: String) {
        failingUnregisterIDs.remove(installationID)
    }
}

private actor BlockingRegistrationTransport: NotificationInstallationTransport {
    private var registrationIDs: [String]
    private var firstRegistrationStarted = false
    private var firstRegistrationReleased = false
    private var startWaiters: [CheckedContinuation<Void, Never>] = []
    private var releaseWaiter: CheckedContinuation<Void, Never>?
    private(set) var registrationCount = 0
    private(set) var unregisteredInstallationIDs: [String] = []

    init(registrationIDs: [String]) {
        self.registrationIDs = registrationIDs
    }

    func register(
        _ request: NotificationRegistrationRequest
    ) async -> NotificationInstallationReceipt {
        registrationCount += 1
        if registrationCount == 1 {
            firstRegistrationStarted = true
            for waiter in startWaiters {
                waiter.resume()
            }
            startWaiters.removeAll()
            if !firstRegistrationReleased {
                await withCheckedContinuation { continuation in
                    releaseWaiter = continuation
                }
            }
        }
        return NotificationInstallationReceipt(
            installationID: registrationIDs.removeFirst()
        )
    }

    func heartbeat(
        installationID: String,
        request: NotificationHeartbeatRequest
    ) -> NotificationInstallationReceipt {
        NotificationInstallationReceipt(installationID: installationID)
    }

    func unregister(installationID: String) {
        unregisteredInstallationIDs.append(installationID)
    }

    func waitUntilFirstRegistrationStarts() async {
        guard !firstRegistrationStarted else { return }
        await withCheckedContinuation { continuation in
            startWaiters.append(continuation)
        }
    }

    func releaseFirstRegistration() {
        firstRegistrationReleased = true
        releaseWaiter?.resume()
        releaseWaiter = nil
    }
}

private actor TestSignal {
    private var signaled = false
    private var waiters: [CheckedContinuation<Void, Never>] = []

    var isSignaled: Bool { signaled }

    func signal() {
        signaled = true
        for waiter in waiters {
            waiter.resume()
        }
        waiters.removeAll()
    }

    func wait() async {
        guard !signaled else { return }
        await withCheckedContinuation { continuation in
            waiters.append(continuation)
        }
    }
}

private actor EventRecorder {
    enum Event: Equatable {
        case savedPendingUnbind
        case unregister
        case cleared
    }

    private(set) var events: [Event] = []

    func record(_ event: Event) {
        events.append(event)
    }

    func reset() {
        events.removeAll()
    }
}
