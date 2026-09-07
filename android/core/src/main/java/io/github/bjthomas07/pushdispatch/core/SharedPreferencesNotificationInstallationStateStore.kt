package io.github.bjthomas07.pushdispatch.core

import android.content.Context

/** Uninstall-scoped persistence for a single app installation's last server binding/lastSeen. */
class SharedPreferencesNotificationInstallationStateStore(
    context: Context,
    preferencesName: String = DEFAULT_PREFERENCES_NAME,
) : NotificationInstallationStateStore {
    private val preferences = context.applicationContext.getSharedPreferences(
        preferencesName,
        Context.MODE_PRIVATE,
    )

    override fun read(): NotificationInstallationState? {
        val state = runCatching { readState() }.getOrNull()
        if (state == null && preferences.all.isNotEmpty()) clear()
        return state
    }

    private fun readState(): NotificationInstallationState? {
        val accountKey = preferences.getString(KEY_ACCOUNT, null) ?: return null
        val installationId = preferences.getString(KEY_INSTALLATION_ID, null) ?: return null
        val targetValue = preferences.getString(KEY_TARGET, null) ?: return null
        val targetType = preferences.getString(KEY_TARGET_TYPE, null)
            ?.let { raw -> NotificationTargetType.entries.firstOrNull { it.name == raw } } ?: return null
        val provider = preferences.getString(KEY_PROVIDER, null)
            ?.let { raw -> NotificationProvider.entries.firstOrNull { it.name == raw } } ?: return null
        val platform = preferences.getString(KEY_PLATFORM, null) ?: return null
        val permission = preferences.getString(KEY_PERMISSION, null)
            ?.let { raw -> NotificationPermission.entries.firstOrNull { it.name == raw } } ?: return null
        val timezone = preferences.getString(KEY_TIMEZONE, null) ?: return null
        if (!preferences.contains(KEY_LAST_SEEN)) return null

        val pendingCleanupInstallationIds = preferences
            .getStringSet(KEY_PENDING_CLEANUP_IDS, emptySet())
            .orEmpty()
            .toList()
            .sorted()

        return NotificationInstallationState(
            accountKey = accountKey,
            installationId = installationId,
            target = NotificationTarget(provider, targetType, targetValue),
            metadata = NotificationInstallationMetadata(
                platform = platform,
                permission = permission,
                timezone = timezone,
                enabled = preferences.getBoolean(KEY_ENABLED, true),
            ),
            lastSeenAtEpochMillis = preferences.getLong(KEY_LAST_SEEN, 0L),
            pendingCleanupInstallationIds = pendingCleanupInstallationIds,
        )
    }

    override fun write(state: NotificationInstallationState) {
        preferences.edit()
            .putString(KEY_ACCOUNT, state.accountKey)
            .putString(KEY_INSTALLATION_ID, state.installationId)
            .putString(KEY_PROVIDER, state.target.provider.name)
            .putString(KEY_TARGET_TYPE, state.target.type.name)
            .putString(KEY_TARGET, state.target.value)
            .putString(KEY_PLATFORM, state.metadata.platform)
            .putString(KEY_PERMISSION, state.metadata.permission.name)
            .putString(KEY_TIMEZONE, state.metadata.timezone)
            .putBoolean(KEY_ENABLED, state.metadata.enabled)
            .putLong(KEY_LAST_SEEN, state.lastSeenAtEpochMillis)
            .putStringSet(
                KEY_PENDING_CLEANUP_IDS,
                state.pendingCleanupInstallationIds.toSet(),
            )
            .apply()
    }

    override fun clear() {
        preferences.edit().clear().apply()
    }

    companion object {
        private const val DEFAULT_PREFERENCES_NAME = "notification_installation_v1"
        private const val KEY_ACCOUNT = "account"
        private const val KEY_INSTALLATION_ID = "installation_id"
        private const val KEY_PROVIDER = "provider"
        private const val KEY_TARGET_TYPE = "target_type"
        private const val KEY_TARGET = "target"
        private const val KEY_PLATFORM = "platform"
        private const val KEY_PERMISSION = "permission"
        private const val KEY_TIMEZONE = "timezone"
        private const val KEY_ENABLED = "enabled"
        private const val KEY_LAST_SEEN = "last_seen_at"
        private const val KEY_PENDING_CLEANUP_IDS = "pending_cleanup_ids"
    }
}
