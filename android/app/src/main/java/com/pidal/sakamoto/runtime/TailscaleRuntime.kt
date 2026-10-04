package com.pidal.sakamoto.runtime

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * UI state for the Tailscale page. Aggregate values only — raw share-class
 * secrets never appear here (the auth key lives in TailscaleAuthKeyStore and
 * is surfaced as hasAuthKey + a masked preview at most).
 */
data class TailscaleUiState(
    val subscribed: Boolean = false,
    val endpoints: List<TailscaleEndpointSummary> = emptyList(),
    val hasAuthKey: Boolean = false,
    val authKeyMasked: String = "",
    val pingLines: List<String> = emptyList(),
    val notice: String? = null,
) {
    /** The endpoint driving the login flow (first NeedsLogin one, else first). */
    val primary: TailscaleEndpointSummary?
        get() = endpoints.firstOrNull { it.backendState.needsLoginFlow && it.authURL.isNotEmpty() }
            ?: endpoints.firstOrNull()

    val authURL: String get() = primary?.authURL.orEmpty()
}

/**
 * Single owner of Tailscale page state (the Android mirror of the iOS
 * TailscaleController's presentation state, minus the commanding channel —
 * libbox RPCs are issued from CommandClientRuntime).
 *
 * Pure folds live in TailscaleModels; the libbox type mapping lives in
 * TailscaleBinding (glue, pending SDK build verification).
 */
object TailscaleRuntime {

    private val _state = MutableStateFlow(TailscaleUiState())
    val state: StateFlow<TailscaleUiState> = _state.asStateFlow()

    private val lock = Any()

    fun setSubscribed(subscribed: Boolean) {
        synchronized(lock) { _state.value = _state.value.copy(subscribed = subscribed) }
    }

    /** Apply one status update (already mapped to pure summaries). */
    fun applyEndpoints(endpoints: List<TailscaleEndpointSummary>) {
        synchronized(lock) {
            _state.value = _state.value.copy(endpoints = endpoints, notice = null)
        }
    }

    fun setAuthKeyState(hasKey: Boolean, masked: String) {
        synchronized(lock) {
            _state.value = _state.value.copy(hasAuthKey = hasKey, authKeyMasked = masked)
        }
    }

    /** Newest ping line first, bounded so the page cannot grow unbounded. */
    fun appendPing(outcome: TailscalePingOutcome) {
        synchronized(lock) {
            val current = _state.value
            _state.value = current.copy(pingLines = (listOf(outcome.render()) + current.pingLines).take(20))
        }
    }

    fun setNotice(message: String) {
        synchronized(lock) { _state.value = _state.value.copy(notice = message) }
    }

    fun reset() {
        synchronized(lock) {
            _state.value = TailscaleUiState(
                hasAuthKey = _state.value.hasAuthKey,
                authKeyMasked = _state.value.authKeyMasked,
            )
        }
    }
}
