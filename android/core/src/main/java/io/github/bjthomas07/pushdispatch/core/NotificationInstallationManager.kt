package io.github.bjthomas07.pushdispatch.core

import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/** The local-only account key prevents a cached binding from crossing accounts. */
class NotificationInstallationManager(
    private val targetProvider: NotificationTargetProvider,
    private val backend: NotificationInstallationBackend,
    private val stateStore: NotificationInstallationStateStore,
    private val clock: NotificationClock = NotificationClock(System::currentTimeMillis),
    private val heartbeatIntervalMillis: Long = DEFAULT_HEARTBEAT_INTERVAL_MILLIS,
) {
    private val mutex = Mutex()

    init {
        require(heartbeatIntervalMillis >= 0L) { "Heartbeat interval must not be negative" }
    }

    suspend fun bind(
        accountKey: String,
        metadata: NotificationInstallationMetadata,
        forceHeartbeat: Boolean = false,
    ): NotificationInstallationState = mutex.withLock {
        require(accountKey.isNotBlank()) { "Account key must not be blank" }
        val current = stateStore.read()
        if (current != null && current.accountKey != accountKey) {
            throw NotificationAccountTransitionException()
        }
        val target = targetProvider.register()
        val now = clock.nowEpochMillis()
        val sameBinding = current?.accountKey == accountKey && current.target == target

        if (!sameBinding) {
            val installationId = backend.bind(target, metadata)
            require(installationId.isNotBlank()) { "Backend installation id must not be blank" }

            // Persist the replacement before retiring the old target. If cleanup is
            // interrupted, the replacement and every stale ID remain available for
            // retry after a restart or account transition.
            val pendingCleanup = buildList {
                if (current != null && current.installationId != installationId) {
                    add(current.installationId)
                }
                current?.pendingCleanupInstallationIds.orEmpty().forEach(::add)
            }.distinct().filterNot { it == installationId }.sorted()
            val replacement = NotificationInstallationState(
                accountKey = accountKey,
                installationId = installationId,
                target = target,
                metadata = metadata,
                lastSeenAtEpochMillis = now,
                pendingCleanupInstallationIds = pendingCleanup,
            )
            stateStore.write(replacement)
            cleanupPendingInstallations(replacement)
            return@withLock stateStore.read() ?: replacement
        }

        checkNotNull(current)
        val cleaned = cleanupPendingInstallations(current)
        val heartbeatDue = now - cleaned.lastSeenAtEpochMillis >= heartbeatIntervalMillis
        if (forceHeartbeat || heartbeatDue || current.metadata != metadata) {
            try {
                backend.heartbeat(cleaned.installationId, metadata)
            } catch (_: NotificationInstallationNotFoundException) {
                val replacementId = backend.bind(cleaned.target, metadata)
                require(replacementId.isNotBlank()) { "Backend installation id must not be blank" }
                val replacement = cleaned.copy(
                    installationId = replacementId,
                    metadata = metadata,
                    lastSeenAtEpochMillis = now,
                    // A 404 heartbeat proves the previous current installation
                    // is already gone, so it does not need stale cleanup.
                    pendingCleanupInstallationIds = cleaned.pendingCleanupInstallationIds
                        .filterNot { it == replacementId }
                        .distinct()
                        .sorted(),
                )
                stateStore.write(replacement)
                cleanupPendingInstallations(replacement)
                return@withLock stateStore.read() ?: replacement
            }
            return@withLock cleaned.copy(
                metadata = metadata,
                lastSeenAtEpochMillis = now,
            ).also(stateStore::write)
        }
        cleaned
    }

    /** Retains local state when authenticated server unbind fails so it can be retried. */
    suspend fun unbind(accountKey: String): Boolean = mutex.withLock {
        val current = stateStore.read() ?: return@withLock true
        if (current.accountKey != accountKey) return@withLock false

        // Keep the state until every active and stale installation has been
        // retired. A failed delete therefore blocks account changes but can be
        // retried without losing the replacement ID.
        val installationIds = buildList {
            add(current.installationId)
            addAll(current.pendingCleanupInstallationIds)
        }.distinct()
        var firstFailure: Exception? = null
        for (installationId in installationIds) {
            try {
                backend.unbind(installationId)
            } catch (error: kotlinx.coroutines.CancellationException) {
                throw error
            } catch (error: Exception) {
                // Try every tracked ID in this pass; the first failure is
                // rethrown after all cleanup attempts so no stale target is
                // skipped merely because the current one failed.
                if (firstFailure == null) firstFailure = error
            }
        }
        firstFailure?.let { throw it }
        stateStore.clear()
        true
    }

    /** Clears local state only after the owning server account has been deleted. */
    suspend fun clearDeletedAccount(accountKey: String): Boolean = mutex.withLock {
        require(accountKey.isNotBlank()) { "Account key must not be blank" }
        val current = stateStore.read() ?: return@withLock true
        if (current.accountKey != accountKey) return@withLock false
        stateStore.clear()
        true
    }

    fun currentState(): NotificationInstallationState? = stateStore.read()

    companion object {
        const val DEFAULT_HEARTBEAT_INTERVAL_MILLIS: Long = 24L * 60L * 60L * 1_000L
    }

    private suspend fun cleanupPendingInstallations(
        state: NotificationInstallationState,
    ): NotificationInstallationState {
        if (state.pendingCleanupInstallationIds.isEmpty()) return state
        val remaining = buildList {
            for (installationId in state.pendingCleanupInstallationIds) {
                try {
                    backend.unbind(installationId)
                } catch (error: kotlinx.coroutines.CancellationException) {
                    throw error
                } catch (_: Exception) {
                    add(installationId)
                }
            }
        }.distinct().sorted()
        if (remaining == state.pendingCleanupInstallationIds) return state
        return state.copy(pendingCleanupInstallationIds = remaining).also(stateStore::write)
    }
}
