package io.github.bjthomas07.pushdispatch.firebase

import android.app.NotificationManager
import io.github.bjthomas07.pushdispatch.core.NotificationMessage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class FirebaseNotificationPresenterTest {
    @Test
    fun `permission revocation during notify drops the notification without crashing`() {
        assertFalse(
            postNotificationSafely {
                throw SecurityException("permission revoked")
            },
        )
    }

    @Test
    fun `successful notify reports displayed`() {
        var posted = false

        assertTrue(postNotificationSafely { posted = true })
        assertTrue(posted)
    }

    @Test
    fun `delivery requires runtime app and channel permission`() {
        assertFalse(
            firebaseNotificationDeliveryAllowed(
                runtimePermissionGranted = false,
                appNotificationsEnabled = true,
                channelImportance = NotificationManager.IMPORTANCE_HIGH,
            ),
        )
        assertFalse(
            firebaseNotificationDeliveryAllowed(
                runtimePermissionGranted = true,
                appNotificationsEnabled = false,
                channelImportance = NotificationManager.IMPORTANCE_HIGH,
            ),
        )
        assertFalse(
            firebaseNotificationDeliveryAllowed(
                runtimePermissionGranted = true,
                appNotificationsEnabled = true,
                channelImportance = NotificationManager.IMPORTANCE_NONE,
            ),
        )
        assertTrue(
            firebaseNotificationDeliveryAllowed(
                runtimePermissionGranted = true,
                appNotificationsEnabled = true,
                channelImportance = NotificationManager.IMPORTANCE_HIGH,
            ),
        )
    }

    @Test
    fun `missing channel is not deliverable`() {
        assertFalse(
            firebaseNotificationDeliveryAllowed(
                runtimePermissionGranted = true,
                appNotificationsEnabled = true,
                channelImportance = null,
            ),
        )
    }

    @Test
    fun `collapse key keeps retry presentation identity stable`() {
        val first = message(
            messageId = "delivery-1",
            body = "First delivery",
            collapseKey = "morning-reminder",
        )
        val retry = message(
            messageId = "delivery-2",
            body = "Retried delivery",
            collapseKey = "morning-reminder",
        )

        assertEquals(first.presentationId(), retry.presentationId())
    }

    @Test
    fun `message id then body provide presentation identity fallbacks`() {
        val byMessageId = message(messageId = "delivery-1", body = "Reminder")
        val sameMessageId = message(messageId = "delivery-1", body = "Updated reminder")
        val byBody = message(messageId = null, body = "Reminder")

        assertEquals(byMessageId.presentationId(), sameMessageId.presentationId())
        assertEquals("Reminder".hashCode(), byBody.presentationId())
        assertNotEquals(byMessageId.presentationId(), byBody.presentationId())
    }

    private fun message(
        messageId: String?,
        body: String,
        collapseKey: String? = null,
    ) = NotificationMessage(
        messageId = messageId,
        title = "Example App",
        body = body,
        deepLink = "exampleapp://today",
        data = emptyMap(),
        collapseKey = collapseKey,
    )
}
