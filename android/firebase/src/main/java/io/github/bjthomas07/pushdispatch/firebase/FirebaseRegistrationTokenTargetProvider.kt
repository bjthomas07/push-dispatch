package io.github.bjthomas07.pushdispatch.firebase

import io.github.bjthomas07.pushdispatch.core.NotificationProvider
import io.github.bjthomas07.pushdispatch.core.NotificationTarget
import io.github.bjthomas07.pushdispatch.core.NotificationTargetProvider
import io.github.bjthomas07.pushdispatch.core.NotificationTargetType
import com.google.android.gms.tasks.Task
import com.google.firebase.messaging.FirebaseMessaging
import java.util.concurrent.atomic.AtomicReference
import kotlin.coroutines.resumeWithException
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeoutOrNull

/** Returns the current FCM registration token and tracks token rotations. */
class FirebaseRegistrationTokenTargetProvider(
    private val fetchToken: suspend () -> String,
    private val registrationTimeoutMillis: Long = DEFAULT_REGISTRATION_TIMEOUT_MILLIS,
) : NotificationTargetProvider {
    private val currentToken = AtomicReference<String?>(null)
    private val registrationMutex = Mutex()

    constructor(
        messaging: FirebaseMessaging,
        registrationTimeoutMillis: Long = DEFAULT_REGISTRATION_TIMEOUT_MILLIS,
    ) : this(
        fetchToken = { messaging.token.awaitResult() },
        registrationTimeoutMillis = registrationTimeoutMillis,
    )

    init {
        require(registrationTimeoutMillis > 0L) { "Registration timeout must be positive" }
    }

    override suspend fun register(): NotificationTarget {
        val token = currentToken.get() ?: registrationMutex.withLock {
            currentToken.get() ?: (
                withTimeoutOrNull(registrationTimeoutMillis) {
                    fetchToken().normalizedToken()
                } ?: throw IllegalStateException("Firebase registration token request timed out")
            ).let { fetchedToken ->
                currentToken.compareAndSet(null, fetchedToken)
                checkNotNull(currentToken.get())
            }
        }
        return token.toTarget()
    }

    /** Must be called from [com.google.firebase.messaging.FirebaseMessagingService.onNewToken]. */
    fun onNewToken(token: String) {
        currentToken.set(token.normalizedToken())
    }

    private fun String.toTarget() = NotificationTarget(
        provider = NotificationProvider.FCM,
        type = NotificationTargetType.REGISTRATION_TOKEN,
        value = this,
    )

    private fun String.normalizedToken(): String = trim().also {
        require(it.isNotEmpty()) { "Firebase registration token must not be blank" }
    }

    companion object {
        const val DEFAULT_REGISTRATION_TIMEOUT_MILLIS = 20_000L
    }
}

private suspend fun <T> Task<T>.awaitResult(): T = suspendCancellableCoroutine { continuation ->
    addOnCompleteListener { task ->
        when {
            task.isSuccessful -> continuation.resumeWith(Result.success(task.result))
            task.isCanceled -> continuation.cancel()
            else -> continuation.resumeWithException(
                task.exception ?: IllegalStateException("Firebase task failed without an exception"),
            )
        }
    }
}
