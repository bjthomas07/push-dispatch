package io.github.bjthomas07.pushdispatch.core

import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class NotificationInstallationManagerTest {
    private val target = NotificationTarget(
        provider = NotificationProvider.FCM,
        type = NotificationTargetType.REGISTRATION_TOKEN,
        value = "token-1",
    )
    private val metadata = NotificationInstallationMetadata(
        platform = "android",
        permission = NotificationPermission.GRANTED,
        timezone = "America/New_York",
        enabled = true,
    )

    @Test
    fun `bind heartbeats after interval and unbinds before clearing state`() = runTest {
        val events = mutableListOf<String>()
        val backend = FakeBackend(events)
        val store = FakeStore()
        val clock = MutableClock(1_000L)
        val manager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { target },
            backend = backend,
            stateStore = store,
            clock = clock,
            heartbeatIntervalMillis = 100L,
        )

        manager.bind("user-a", metadata)
        clock.now = 1_050L
        manager.bind("user-a", metadata)
        clock.now = 1_101L
        manager.bind("user-a", metadata)
        manager.unbind("user-a")

        assertEquals(listOf("bind:token-1", "heartbeat:installation-1", "unbind:installation-1"), events)
        assertNull(store.state)
    }

    @Test
    fun `changed account is blocked until prior account unbind succeeds`() = runTest {
        val events = mutableListOf<String>()
        val manager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { target },
            backend = FakeBackend(events),
            stateStore = FakeStore(),
        )

        manager.bind("user-a", metadata)
        val failed = runCatching { manager.bind("user-b", metadata) }.exceptionOrNull()

        assertTrue(failed is NotificationAccountTransitionException)
        assertEquals(listOf("bind:token-1"), events)
        assertEquals("user-a", manager.currentState()?.accountKey)
    }

    @Test
    fun `token rotation binds replacement then retires prior installation`() = runTest {
        val events = mutableListOf<String>()
        var currentTarget = target
        val manager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { currentTarget },
            backend = FakeBackend(events),
            stateStore = FakeStore(),
        )
        manager.bind("user-a", metadata)
        currentTarget = target.copy(value = "token-2")

        manager.bind("user-a", metadata)

        assertEquals(
            listOf("bind:token-1", "bind:token-2", "unbind:installation-1"),
            events,
        )
        assertEquals("token-2", manager.currentState()?.target?.value)
    }

    @Test
    fun `token rotation persists replacement when stale cleanup fails and retries it`() = runTest {
        val events = mutableListOf<String>()
        val backend = FakeBackend(events)
        val store = FakeStore()
        val manager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { target },
            backend = backend,
            stateStore = store,
        )
        manager.bind("user-a", metadata)

        var currentTarget = target.copy(value = "token-2")
        val rotatingManager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { currentTarget },
            backend = backend,
            stateStore = store,
        )
        backend.failingUnbindInstallationIds += "installation-1"
        rotatingManager.bind("user-a", metadata)

        assertEquals("installation-2", rotatingManager.currentState()?.installationId)
        assertEquals(listOf("installation-1"), rotatingManager.currentState()?.pendingCleanupInstallationIds)

        backend.failingUnbindInstallationIds.clear()
        rotatingManager.bind("user-a", metadata)

        assertEquals(
            listOf("bind:token-1", "bind:token-2", "unbind:installation-1", "unbind:installation-1"),
            events,
        )
        assertTrue(rotatingManager.currentState()?.pendingCleanupInstallationIds.orEmpty().isEmpty())
    }

    @Test
    fun `replacement cleanup retries after manager restart and target rotation back`() = runTest {
        val events = mutableListOf<String>()
        val backend = FakeBackend(events)
        val store = FakeStore()
        var currentTarget = target
        val manager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { currentTarget },
            backend = backend,
            stateStore = store,
        )
        manager.bind("user-a", metadata)

        backend.failingUnbindInstallationIds += "installation-1"
        currentTarget = target.copy(value = "token-2")
        manager.bind("user-a", metadata)
        currentTarget = target

        val restartedManager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { currentTarget },
            backend = backend,
            stateStore = store,
        )
        restartedManager.bind("user-a", metadata)

        assertEquals("installation-3", restartedManager.currentState()?.installationId)
        assertEquals(
            listOf("installation-1"),
            restartedManager.currentState()?.pendingCleanupInstallationIds,
        )
        assertEquals(
            listOf(
                "bind:token-1",
                "bind:token-2",
                "unbind:installation-1",
                "bind:token-1",
                "unbind:installation-1",
                "unbind:installation-2",
            ),
            events,
        )

        backend.failingUnbindInstallationIds.clear()
        restartedManager.bind("user-a", metadata)
        assertTrue(restartedManager.currentState()?.pendingCleanupInstallationIds.orEmpty().isEmpty())
    }

    @Test
    fun `unbind retires replacement and every pending stale installation before clearing`() = runTest {
        val events = mutableListOf<String>()
        val backend = FakeBackend(events)
        val store = FakeStore(
            NotificationInstallationState(
                accountKey = "user-a",
                installationId = "installation-2",
                target = target.copy(value = "token-2"),
                metadata = metadata,
                lastSeenAtEpochMillis = 1_000L,
                pendingCleanupInstallationIds = listOf("installation-1"),
            ),
        )
        val manager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { target.copy(value = "token-2") },
            backend = backend,
            stateStore = store,
        )
        backend.failingUnbindInstallationIds += "installation-1"

        assertTrue(runCatching { manager.unbind("user-a") }.isFailure)
        assertEquals("installation-2", manager.currentState()?.installationId)
        assertEquals(listOf("installation-1"), manager.currentState()?.pendingCleanupInstallationIds)

        backend.failingUnbindInstallationIds.clear()
        assertTrue(manager.unbind("user-a"))
        assertNull(manager.currentState())
        assertEquals(
            listOf(
                "unbind:installation-2",
                "unbind:installation-1",
                "unbind:installation-2",
                "unbind:installation-1",
            ),
            events,
        )
    }

    @Test
    fun `unbind attempts pending stale IDs even when current installation fails`() = runTest {
        val events = mutableListOf<String>()
        val backend = FakeBackend(events)
        val store = FakeStore(
            NotificationInstallationState(
                accountKey = "user-a",
                installationId = "installation-2",
                target = target.copy(value = "token-2"),
                metadata = metadata,
                lastSeenAtEpochMillis = 1_000L,
                pendingCleanupInstallationIds = listOf("installation-1"),
            ),
        )
        val manager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { target.copy(value = "token-2") },
            backend = backend,
            stateStore = store,
        )
        backend.failingUnbindInstallationIds += "installation-2"

        assertTrue(runCatching { manager.unbind("user-a") }.isFailure)
        assertEquals(
            listOf("unbind:installation-2", "unbind:installation-1"),
            events,
        )
        assertEquals("installation-2", manager.currentState()?.installationId)
    }

    @Test
    fun `unchanged startup registrations stay throttled while changed target binds immediately`() = runTest {
        val events = mutableListOf<String>()
        val store = FakeStore()
        val clock = MutableClock(1_000L)
        var currentTarget = target.copy(
            type = NotificationTargetType.FIREBASE_INSTALLATION_ID,
            value = "fid-1",
        )
        val backend = FakeBackend(events)
        val initialManager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { currentTarget },
            backend = backend,
            stateStore = store,
            clock = clock,
            heartbeatIntervalMillis = 100L,
        )
        initialManager.bind("user-a", metadata)

        clock.now = 1_050L
        val restartedManager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { currentTarget },
            backend = backend,
            stateStore = store,
            clock = clock,
            heartbeatIntervalMillis = 100L,
        )
        listOf(
            async { restartedManager.bind("user-a", metadata) },
            async { restartedManager.bind("user-a", metadata) },
        ).awaitAll()

        currentTarget = currentTarget.copy(value = "fid-2")
        restartedManager.bind("user-a", metadata)

        assertEquals(
            listOf("bind:fid-1", "bind:fid-2", "unbind:installation-1"),
            events,
        )
        assertEquals("fid-2", restartedManager.currentState()?.target?.value)
    }

    @Test
    fun `server-pruned installation at device cap re-registers and persists replacement`() = runTest {
        val events = mutableListOf<String>()
        val staleState = NotificationInstallationState(
            accountKey = "user-a",
            installationId = "installation-pruned",
            target = target,
            metadata = metadata,
            lastSeenAtEpochMillis = 1_000L,
        )
        val store = FakeStore(staleState)
        val backend = FakeBackend(events).apply {
            heartbeatFailure = NotificationInstallationNotFoundException()
        }
        val manager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { target },
            backend = backend,
            stateStore = store,
            clock = NotificationClock { 1_101L },
            heartbeatIntervalMillis = 100L,
        )

        val recovered = manager.bind("user-a", metadata)

        assertEquals(
            listOf("heartbeat:installation-pruned", "bind:token-1"),
            events,
        )
        assertEquals("installation-1", recovered.installationId)
        assertEquals("installation-1", store.state?.installationId)
        assertEquals(1_101L, store.state?.lastSeenAtEpochMillis)
    }

    @Test
    fun `replacement bind failure preserves pruned installation for retry`() = runTest {
        val events = mutableListOf<String>()
        val staleState = NotificationInstallationState(
            accountKey = "user-a",
            installationId = "installation-pruned",
            target = target,
            metadata = metadata,
            lastSeenAtEpochMillis = 1_000L,
        )
        val store = FakeStore(staleState)
        val backend = FakeBackend(events).apply {
            heartbeatFailure = NotificationInstallationNotFoundException()
            bindFailure = IllegalStateException("offline")
        }
        val manager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { target },
            backend = backend,
            stateStore = store,
            clock = NotificationClock { 1_101L },
            heartbeatIntervalMillis = 100L,
        )

        val failure = runCatching { manager.bind("user-a", metadata) }.exceptionOrNull()

        assertEquals("offline", failure?.message)
        assertEquals(
            listOf("heartbeat:installation-pruned", "bind:token-1"),
            events,
        )
        assertEquals(staleState, store.state)
    }

    @Test
    fun `ordinary heartbeat failure does not re-register`() = runTest {
        val events = mutableListOf<String>()
        val store = FakeStore()
        val backend = FakeBackend(events)
        val manager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { target },
            backend = backend,
            stateStore = store,
            heartbeatIntervalMillis = 0L,
        )
        manager.bind("user-a", metadata)
        backend.heartbeatFailure = IllegalStateException("offline")

        val failure = runCatching { manager.bind("user-a", metadata) }.exceptionOrNull()

        assertEquals("offline", failure?.message)
        assertEquals(listOf("bind:token-1", "heartbeat:installation-1"), events)
        assertEquals("installation-1", store.state?.installationId)
    }

    @Test
    fun `unbind failure retains binding for retry and never clears before backend`() = runTest {
        val events = mutableListOf<String>()
        val backend = FakeBackend(events, failUnbind = true)
        val store = FakeStore()
        val manager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { target },
            backend = backend,
            stateStore = store,
        )
        manager.bind("user-a", metadata)

        val failed = runCatching { manager.unbind("user-a") }.isFailure

        assertTrue(failed)
        assertEquals(listOf("bind:token-1", "unbind:installation-1"), events)
        assertEquals("user-a", store.state?.accountKey)
        assertFalse(manager.unbind("another-user"))
    }

    @Test
    fun `confirmed account deletion clears failed unbind state for next account`() = runTest {
        val events = mutableListOf<String>()
        val backend = FakeBackend(events, failUnbind = true)
        val store = FakeStore()
        val manager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { target },
            backend = backend,
            stateStore = store,
        )
        manager.bind("user-a", metadata)
        runCatching { manager.unbind("user-a") }

        assertTrue(manager.clearDeletedAccount("user-a"))
        manager.bind("user-b", metadata)

        assertEquals(
            listOf("bind:token-1", "unbind:installation-1", "bind:token-1"),
            events,
        )
        assertEquals("user-b", manager.currentState()?.accountKey)
    }

    @Test
    fun `deleted-account cleanup never clears another account state`() = runTest {
        val manager = NotificationInstallationManager(
            targetProvider = NotificationTargetProvider { target },
            backend = FakeBackend(mutableListOf()),
            stateStore = FakeStore(),
        )
        manager.bind("user-a", metadata)

        assertFalse(manager.clearDeletedAccount("user-b"))
        assertEquals("user-a", manager.currentState()?.accountKey)
    }

    private class MutableClock(var now: Long) : NotificationClock {
        override fun nowEpochMillis(): Long = now
    }

    private class FakeStore(
        var state: NotificationInstallationState? = null,
    ) : NotificationInstallationStateStore {
        override fun read(): NotificationInstallationState? = state
        override fun write(state: NotificationInstallationState) {
            this.state = state
        }
        override fun clear() {
            state = null
        }
    }

    private class FakeBackend(
        private val events: MutableList<String>,
        private val failUnbind: Boolean = false,
    ) : NotificationInstallationBackend {
        private var bindCount = 0
        var bindFailure: Throwable? = null
        var heartbeatFailure: Throwable? = null
        val failingUnbindInstallationIds = mutableSetOf<String>()

        override suspend fun bind(
            target: NotificationTarget,
            metadata: NotificationInstallationMetadata,
        ): String {
            events += "bind:${target.value}"
            bindFailure?.let { throw it }
            bindCount += 1
            return "installation-$bindCount"
        }

        override suspend fun heartbeat(
            installationId: String,
            metadata: NotificationInstallationMetadata,
        ) {
            events += "heartbeat:$installationId"
            heartbeatFailure?.let { throw it }
        }

        override suspend fun unbind(installationId: String) {
            events += "unbind:$installationId"
            if (failUnbind || installationId in failingUnbindInstallationIds) error("offline")
        }
    }

}
