package io.github.bjthomas07.pushdispatch.core

enum class NotificationProvider(val wireValue: String) {
    FCM("fcm"),
}

enum class NotificationTargetType(val wireValue: String) {
    FIREBASE_INSTALLATION_ID("fid"),
    REGISTRATION_TOKEN("token"),
}

data class NotificationTarget(
    val provider: NotificationProvider,
    val type: NotificationTargetType,
    val value: String,
) {
    init {
        require(value.isNotBlank()) { "Notification target must not be blank" }
    }
}

enum class NotificationPermission(val wireValue: String) {
    GRANTED("granted"),
    DENIED("denied"),
    UNKNOWN("unknown"),
}

data class NotificationInstallationMetadata(
    val platform: String,
    val permission: NotificationPermission,
    val timezone: String,
    val enabled: Boolean,
) {
    init {
        require(platform.isNotBlank()) { "Notification platform must not be blank" }
        require(timezone.isNotBlank()) { "Notification timezone must not be blank" }
    }
}

data class NotificationInstallationState(
    /** Local-only account key. Authentication on the HTTP request performs the actual binding. */
    val accountKey: String,
    val installationId: String,
    val target: NotificationTarget,
    val metadata: NotificationInstallationMetadata,
    val lastSeenAtEpochMillis: Long,
    /** Installation IDs that were replaced but whose backend cleanup still needs a retry. */
    val pendingCleanupInstallationIds: List<String> = emptyList(),
)

data class NotificationMessage(
    val messageId: String?,
    val title: String?,
    val body: String?,
    val deepLink: String?,
    val data: Map<String, String>,
    val collapseKey: String? = null,
)

fun interface NotificationTargetProvider {
    suspend fun register(): NotificationTarget
}

class NotificationAccountTransitionException : IllegalStateException(
    "Notification installation is still bound to another account; unbind must succeed first",
)

/** Backend adapters throw this when heartbeat finds no stored installation. */
class NotificationInstallationNotFoundException(
    cause: Throwable? = null,
) : Exception("Notification installation no longer exists", cause)

interface NotificationInstallationBackend {
    /** Creates or re-binds the target to the account represented by the current auth context. */
    suspend fun bind(
        target: NotificationTarget,
        metadata: NotificationInstallationMetadata,
    ): String

    suspend fun heartbeat(
        installationId: String,
        metadata: NotificationInstallationMetadata,
    )

    suspend fun unbind(installationId: String)
}

interface NotificationInstallationStateStore {
    fun read(): NotificationInstallationState?
    fun write(state: NotificationInstallationState)
    fun clear()
}

fun interface NotificationClock {
    fun nowEpochMillis(): Long
}
