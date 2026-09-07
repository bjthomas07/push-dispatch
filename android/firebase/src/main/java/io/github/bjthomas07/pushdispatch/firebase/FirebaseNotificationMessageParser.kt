package io.github.bjthomas07.pushdispatch.firebase

import io.github.bjthomas07.pushdispatch.core.NotificationMessage
import com.google.firebase.messaging.RemoteMessage

object FirebaseNotificationMessageParser {
    fun parse(message: RemoteMessage): NotificationMessage = parse(
        data = message.data,
        notificationTitle = message.notification?.title,
        notificationBody = message.notification?.body,
        messageId = message.messageId,
        collapseKey = message.collapseKey,
    )

    fun parse(
        data: Map<String, String>,
        notificationTitle: String? = null,
        notificationBody: String? = null,
        messageId: String? = null,
        collapseKey: String? = null,
    ): NotificationMessage = NotificationMessage(
        messageId = messageId,
        title = data.firstValue("title", "notificationTitle") ?: notificationTitle,
        body = data.firstValue("body", "message", "notificationBody") ?: notificationBody,
        deepLink = data.firstValue("deepLink", "deep_link", "deeplink"),
        data = data,
        collapseKey = collapseKey?.trim()?.takeIf(String::isNotEmpty),
    )

    private fun Map<String, String>.firstValue(vararg keys: String): String? = keys
        .firstNotNullOfOrNull { key -> get(key)?.trim()?.takeIf(String::isNotEmpty) }
}
