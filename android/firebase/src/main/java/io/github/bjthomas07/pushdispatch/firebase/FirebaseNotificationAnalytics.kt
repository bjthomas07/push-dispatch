package io.github.bjthomas07.pushdispatch.firebase

import android.content.Intent
import io.github.bjthomas07.pushdispatch.core.NotificationMessage

data class FirebaseNotificationAttribution(
    val notificationId: String,
    val app: String?,
    val kind: String?,
    val analyticsLabel: String?,
    val schemaVersion: String?,
) {
    fun parameters(presentation: String): Map<String, Any> = buildMap {
        put("notification_id", notificationId)
        app?.let { put("notification_app", it) }
        kind?.let { put("notification_kind", it) }
        analyticsLabel?.let { put("analytics_label", it) }
        schemaVersion?.let { put("schema_version", it) }
        put("presentation", presentation)
        put("provider", "fcm")
    }
}

object FirebaseNotificationAnalytics {
    const val EVENT_DISPLAY = "push_notification_display"
    const val EVENT_OPEN = "push_notification_open"
    const val PRESENTATION_APP = "app"

    private const val NOTIFICATION_ID = "notification_id"
    private const val NOTIFICATION_APP = "app"
    private const val NOTIFICATION_KIND = "kind"
    private const val ANALYTICS_LABEL = "analytics_label"
    private const val SCHEMA_VERSION = "schema_version"
    private const val APP_RENDERED = "io.github.bjthomas07.pushdispatch.APP_RENDERED"
    private const val OPEN_RECORDED = "io.github.bjthomas07.pushdispatch.OPEN_RECORDED"
    private val canonicalKeys = listOf(
        NOTIFICATION_ID,
        NOTIFICATION_APP,
        NOTIFICATION_KIND,
        ANALYTICS_LABEL,
        SCHEMA_VERSION,
    )

    fun attribution(message: NotificationMessage): FirebaseNotificationAttribution? =
        attribution(message.data)

    fun attachAppRenderedAttribution(intent: Intent, message: NotificationMessage) {
        // Kept outside analytics attribution for routing and diagnostics.
        intent.putExtra(FirebaseNotificationExtras.MESSAGE_ID, message.messageId)
        val attributionData = intentAttributionData(message)
        if (attributionData.isEmpty()) return

        intent.putExtra(APP_RENDERED, true)
        attributionData.forEach { (key, value) -> intent.putExtra(key, value) }
    }

    fun consumeAppRenderedOpen(intent: Intent?): FirebaseNotificationAttribution? {
        intent ?: return null
        if (!shouldConsumeAppRenderedOpen(
                appRendered = intent.getBooleanExtra(APP_RENDERED, false),
                openRecorded = intent.getBooleanExtra(OPEN_RECORDED, false),
            )
        ) {
            return null
        }
        val attribution = attribution(
            canonicalKeys.mapNotNull { key ->
                intent.getStringExtra(key)?.let { value -> key to value }
            }.toMap(),
        ) ?: return null
        intent.putExtra(OPEN_RECORDED, true)
        return attribution
    }

    internal fun intentAttributionData(message: NotificationMessage): Map<String, String> =
        attribution(message)?.let { attribution ->
            buildMap {
                put(NOTIFICATION_ID, attribution.notificationId)
                attribution.app?.let { put(NOTIFICATION_APP, it) }
                attribution.kind?.let { put(NOTIFICATION_KIND, it) }
                attribution.analyticsLabel?.let { put(ANALYTICS_LABEL, it) }
                attribution.schemaVersion?.let { put(SCHEMA_VERSION, it) }
            }
        }.orEmpty()

    internal fun attribution(data: Map<String, String>): FirebaseNotificationAttribution? = attribution(
        notificationId = data[NOTIFICATION_ID],
        app = data[NOTIFICATION_APP],
        kind = data[NOTIFICATION_KIND],
        analyticsLabel = data[ANALYTICS_LABEL],
        schemaVersion = data[SCHEMA_VERSION],
    )

    internal fun shouldConsumeAppRenderedOpen(
        appRendered: Boolean,
        openRecorded: Boolean,
    ): Boolean = appRendered && !openRecorded

    private fun attribution(
        notificationId: String?,
        app: String?,
        kind: String?,
        analyticsLabel: String?,
        schemaVersion: String?,
    ): FirebaseNotificationAttribution? {
        val id = notificationId.normalized() ?: return null
        return FirebaseNotificationAttribution(
            notificationId = id,
            app = app.normalized(),
            kind = kind.normalized(),
            analyticsLabel = analyticsLabel.normalized(),
            schemaVersion = schemaVersion.normalized(),
        )
    }

    private fun String?.normalized(): String? = this?.trim()?.takeIf(String::isNotEmpty)?.take(100)
}
