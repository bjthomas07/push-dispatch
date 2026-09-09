package io.github.bjthomas07.pushdispatch.firebase

import android.Manifest
import android.annotation.SuppressLint
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import io.github.bjthomas07.pushdispatch.core.NotificationMessage

data class FirebaseNotificationDisplayConfig(
    val channelId: String,
    val channelName: String,
    val defaultTitle: String,
    val smallIconResource: Int,
)

object FirebaseNotificationExtras {
    const val DEEP_LINK = "io.github.bjthomas07.pushdispatch.DEEP_LINK"
    const val MESSAGE_ID = "io.github.bjthomas07.pushdispatch.MESSAGE_ID"
}

/** Displays foreground and data-only background FCM messages using app-supplied launch routing. */
class FirebaseNotificationPresenter(
    context: Context,
    private val config: FirebaseNotificationDisplayConfig,
    private val launchIntentFactory: (NotificationMessage) -> Intent,
) {
    private val applicationContext = context.applicationContext

    fun createChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val manager = applicationContext.getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(
            NotificationChannel(
                config.channelId,
                config.channelName,
                NotificationManager.IMPORTANCE_HIGH,
            ),
        )
    }

    fun show(message: NotificationMessage): Boolean {
        if (!applicationContext.canPostFirebaseNotifications(config.channelId)) {
            return false
        }
        val title = message.title ?: config.defaultTitle
        val body = message.body ?: return false
        val requestCode = message.presentationId()
        val launchIntent = launchIntentFactory(message).apply {
            putExtra(FirebaseNotificationExtras.DEEP_LINK, message.deepLink)
            FirebaseNotificationAnalytics.attachAppRenderedAttribution(this, message)
            addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP)
        }
        val pendingIntent = PendingIntent.getActivity(
            applicationContext,
            requestCode,
            launchIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val notification = NotificationCompat.Builder(applicationContext, config.channelId)
            .setSmallIcon(config.smallIconResource)
            .setContentTitle(title)
            .setContentText(body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setAutoCancel(true)
            .setContentIntent(pendingIntent)
            .build()
        return postNotification(requestCode, notification)
    }

    @SuppressLint("MissingPermission")
    private fun postNotification(
        requestCode: Int,
        notification: android.app.Notification,
    ): Boolean = postNotificationSafely {
        NotificationManagerCompat.from(applicationContext).notify(requestCode, notification)
    }
}

internal fun postNotificationSafely(post: () -> Unit): Boolean = try {
    post()
    true
} catch (_: SecurityException) {
    false
}

internal fun NotificationMessage.presentationId(): Int =
    collapseKey?.takeIf(String::isNotBlank)?.hashCode()
        ?: messageId?.takeIf(String::isNotBlank)?.hashCode()
        ?: body.hashCode()

/**
 * Returns whether Android currently allows this app to post on [channelId].
 * A successful `notify()` call alone is not sufficient: Android can silently
 * discard notifications when the app or channel is disabled.
 */
fun Context.canPostFirebaseNotifications(channelId: String): Boolean {
    val appContext = applicationContext
    val runtimePermissionGranted = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
        ContextCompat.checkSelfPermission(
            appContext,
            Manifest.permission.POST_NOTIFICATIONS,
        ) == PackageManager.PERMISSION_GRANTED
    } else {
        true
    }
    val channelsSupported = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
    val channelImportance = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
        appContext.getSystemService(NotificationManager::class.java)
            ?.getNotificationChannel(channelId)
            ?.importance
    } else {
        null
    }
    return firebaseNotificationDeliveryAllowed(
        runtimePermissionGranted = runtimePermissionGranted,
        appNotificationsEnabled = NotificationManagerCompat.from(appContext).areNotificationsEnabled(),
        channelImportance = channelImportance,
        channelsSupported = channelsSupported,
    )
}

internal fun firebaseNotificationDeliveryAllowed(
    runtimePermissionGranted: Boolean,
    appNotificationsEnabled: Boolean,
    channelImportance: Int?,
    channelsSupported: Boolean = true,
): Boolean = runtimePermissionGranted &&
    appNotificationsEnabled &&
    (!channelsSupported ||
        (channelImportance != null && channelImportance != NotificationManager.IMPORTANCE_NONE))
