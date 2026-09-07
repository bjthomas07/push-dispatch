package io.github.bjthomas07.pushdispatch.firebase

import org.junit.Assert.assertEquals
import org.junit.Test

class FirebaseNotificationMessageParserTest {
    @Test
    fun `data fields override notification fields and preserve deep link`() {
        val parsed = FirebaseNotificationMessageParser.parse(
            data = mapOf(
                "title" to "A quiet invitation",
                "body" to "Come and pray with Jesus.",
                "deepLink" to "exampleapp://content/examen",
                "campaign" to "evening",
            ),
            notificationTitle = "Fallback title",
            notificationBody = "Fallback body",
            messageId = "message-1",
            collapseKey = "evening-reminder",
        )

        assertEquals("A quiet invitation", parsed.title)
        assertEquals("Come and pray with Jesus.", parsed.body)
        assertEquals("exampleapp://content/examen", parsed.deepLink)
        assertEquals("evening", parsed.data["campaign"])
        assertEquals("message-1", parsed.messageId)
        assertEquals("evening-reminder", parsed.collapseKey)
    }

    @Test
    fun `accepts snake case deep links and notification fallback copy`() {
        val parsed = FirebaseNotificationMessageParser.parse(
            data = mapOf("deep_link" to "exampleapp://today"),
            notificationTitle = "Example App",
            notificationBody = "Your daily reminder",
        )

        assertEquals("Example App", parsed.title)
        assertEquals("Your daily reminder", parsed.body)
        assertEquals("exampleapp://today", parsed.deepLink)
    }
}
