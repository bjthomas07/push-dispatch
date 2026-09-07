package io.github.bjthomas07.pushdispatch.firebase

import io.github.bjthomas07.pushdispatch.core.NotificationMessage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class FirebaseNotificationAnalyticsTest {
    @Test
    fun `scheduled payload maps to privacy safe analytics parameters`() {
        val attribution = FirebaseNotificationAnalytics.attribution(
            NotificationMessage(
                messageId = "provider-message",
                title = "Title",
                body = "Body",
                deepLink = "exampleapp://today",
                data = mapOf(
                    "notification_id" to "pl_morning_2026-08-29_1200",
                    "schema_version" to "1",
                    "app" to "exampleapp",
                    "kind" to "morning",
                    "analytics_label" to "pl_morning_2026-08-29",
                ),
            ),
        )

        assertEquals(
            mapOf(
                "notification_id" to "pl_morning_2026-08-29_1200",
                "notification_app" to "exampleapp",
                "notification_kind" to "morning",
                "analytics_label" to "pl_morning_2026-08-29",
                "schema_version" to "1",
                "presentation" to "app",
                "provider" to "fcm",
            ),
            attribution?.parameters(FirebaseNotificationAnalytics.PRESENTATION_APP),
        )
    }

    @Test
    fun `provider message id is not treated as a logical notification id`() {
        assertNull(FirebaseNotificationAnalytics.attribution(message(data = emptyMap())))
    }

    @Test
    fun `canonical intent data round trips without provider message id`() {
        val message = message(
            data = mapOf(
                "notification_id" to "nv_series_day_2026-08-29_1200",
                "schema_version" to "1",
                "app" to "sampleapp",
                "kind" to "series_day",
                "analytics_label" to "nv_series_day_2026-08-29",
            ),
        )

        val serialized = FirebaseNotificationAnalytics.intentAttributionData(message)

        assertEquals(message.data, serialized)
        assertEquals(
            FirebaseNotificationAnalytics.attribution(message),
            FirebaseNotificationAnalytics.attribution(serialized),
        )
        assertFalse(serialized.containsKey("message_id"))
    }

    @Test
    fun `speculative aliases do not create attribution`() {
        assertNull(
            FirebaseNotificationAnalytics.attribution(
                message(data = mapOf("notificationId" to "legacy-id")),
            ),
        )
    }

    @Test
    fun `app rendered marker is consumed once`() {
        assertTrue(FirebaseNotificationAnalytics.shouldConsumeAppRenderedOpen(true, false))
        assertFalse(FirebaseNotificationAnalytics.shouldConsumeAppRenderedOpen(true, true))
        assertFalse(FirebaseNotificationAnalytics.shouldConsumeAppRenderedOpen(false, false))
    }

    private fun message(data: Map<String, String>) = NotificationMessage(
        messageId = "provider-message",
        title = null,
        body = null,
        deepLink = null,
        data = data,
    )
}
