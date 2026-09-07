package io.github.bjthomas07.pushdispatch.firebase

import io.github.bjthomas07.pushdispatch.core.NotificationProvider
import io.github.bjthomas07.pushdispatch.core.NotificationTargetType
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class FirebaseRegistrationTokenTargetProviderTest {
    @Test
    fun `register fetches and returns an FCM token target`() = runTest {
        var fetchCalls = 0
        val provider = FirebaseRegistrationTokenTargetProvider(
            fetchToken = {
                fetchCalls += 1
                "token-phone"
            },
        )

        val target = provider.register()

        assertEquals(NotificationProvider.FCM, target.provider)
        assertEquals(NotificationTargetType.REGISTRATION_TOKEN, target.type)
        assertEquals("token-phone", target.value)
        assertEquals(1, fetchCalls)
    }

    @Test
    fun `token callback replaces cached token without another fetch`() = runTest {
        var fetchCalls = 0
        val provider = FirebaseRegistrationTokenTargetProvider(
            fetchToken = {
                fetchCalls += 1
                "token-first"
            },
        )

        assertEquals("token-first", provider.register().value)
        provider.onNewToken("token-refreshed")
        assertEquals("token-refreshed", provider.register().value)
        assertEquals(1, fetchCalls)
    }

    @Test
    fun `token callback wins over an older in-flight fetch result`() = runTest {
        val fetchStarted = CompletableDeferred<Unit>()
        val fetchedToken = CompletableDeferred<String>()
        val provider = FirebaseRegistrationTokenTargetProvider(
            fetchToken = {
                fetchStarted.complete(Unit)
                fetchedToken.await()
            },
        )

        val target = async { provider.register() }
        fetchStarted.await()
        provider.onNewToken("token-refreshed")
        fetchedToken.complete("token-stale")

        assertEquals("token-refreshed", target.await().value)
    }

    @Test
    fun `registration timeout is a retryable failure rather than parent cancellation`() = runTest {
        val provider = FirebaseRegistrationTokenTargetProvider(
            fetchToken = { awaitCancellation() },
            registrationTimeoutMillis = 1L,
        )

        val failure = runCatching { provider.register() }.exceptionOrNull()

        assertTrue(failure is IllegalStateException)
        assertEquals(false, failure is CancellationException)
    }

    @Test
    fun `outer caller timeout remains cancellation`() = runTest {
        val provider = FirebaseRegistrationTokenTargetProvider(
            fetchToken = { awaitCancellation() },
            registrationTimeoutMillis = 20_000L,
        )

        val failure = runCatching {
            withTimeout(1L) { provider.register() }
        }.exceptionOrNull()

        assertTrue(failure is TimeoutCancellationException)
    }
}
