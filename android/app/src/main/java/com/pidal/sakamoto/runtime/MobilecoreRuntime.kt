package com.pidal.sakamoto.runtime

import com.pidal.sakamoto.mobilecore.Mobilecore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * The Home-page state model.
 *
 * Every word here is the macOS TUI / iOS vocabulary (docs/tui.md); the
 * Android app renders values, it never re-implements the state machine:
 *
 *   serviceState — core.ServiceState ("Stopped", "Starting", "Running",
 *                  "Stopping", "Unavailable"); produced by the tunnel
 *                  service (bg/SakamotoVpnService) and consumed verbatim.
 *   probeState   — core.ProbeState ("Idle", "Checking", "Reachable",
 *                  "Unverified"); derived ONLY through
 *                  Mobilecore.probeStateOf (the gomobile bridge), because the
 *                  bridge owns the rule "one failed probe stays Unverified".
 *   phase        — Mobilecore.sessionPhase(serviceState, probeState,
 *                  conflict): Disconnected / Starting / TUNRunning /
 *                  Reachable / Unverified / Conflict / Unavailable /
 *                  Stopping. TUN running ≠ network reachable is the bridge's
 *                  decision, not ours.
 *   configState  — core.ConfigState ("Clean", "NeedsRegenerate",
 *                  "NeedsReconnect"); advanced by
 *                  Mobilecore.configStateTransition(state, event).
 */
data class RuntimeState(
    val serviceState: String = "Stopped",
    val probeState: String = "Idle",
    val conflict: Boolean = false,
    val routingMode: String = "",
    val phase: String = "Disconnected",
    val selectedNode: String = "",
    val selectedNodeDelay: Int = 0,
    val selectedNodeTesting: Boolean = false,
    val selectedNodeStatus: String = "Untested",
    val configState: String = "Clean",
    val notice: String? = null,
)

/**
 * Single owner of the observations the Home page renders.
 *
 * All semantics flow through the Mobilecore AAR (built from
 * pkg/mobilecore by android/scripts/build-mobilecore.sh) — this object is a
 * thin scheduler around the bridge, exactly like the iOS app's ConfigStore /
 * HomeModel split. There is intentionally NO Kotlin copy of PhaseOf,
 * NextRoutingMode or ClassifyProbe: copying would drift from the TUI.
 */
object MobilecoreRuntime {

    private val _state = MutableStateFlow(RuntimeState())
    val state: StateFlow<RuntimeState> = _state.asStateFlow()

    private val lock = Any()

    private fun apply(transform: (RuntimeState) -> RuntimeState) {
        synchronized(lock) {
            val current = _state.value
            var next = transform(current)
            // The status-bar phase is always the bridge's output.
            next = next.copy(
                phase = Mobilecore.sessionPhase(next.serviceState, next.probeState, next.conflict),
            )
            _state.value = next
        }
    }

    /** Report a core.ServiceState word from the tunnel service. */
    fun setServiceState(value: String) = apply { it.copy(serviceState = value.trim()) }

    /**
     * Classify one probe round. Android has no browser-proxy path, so the
     * proxy leg passes vacuously with proxyEnabled=false and the routing leg
     * carries the result — the bridge then decides Reachable vs Unverified
     * (probeStateOf / probePathOf / probeErrorOf are the bindable flattenings
     * of ClassifyProbe; the struct-returning ClassifyProbe is skipped by
     * gomobile by design, see pkg/mobilecore/mobilecore.go).
     */
    fun recordRouteProbe(routeError: String) = apply {
        val passed = routeError.isEmpty()
        val probeState = Mobilecore.probeStateOf(passed, true, false)
        val detail = if (passed) Mobilecore.probePathOf(passed, true, false) else routeError
        it.copy(probeState = probeState, notice = detail.ifEmpty { null })
    }

    /** Advance the routing-mode cycle: Rule → Global → Direct (the `m` key). */
    fun cycleRoutingMode(): String {
        synchronized(lock) {
            val next = Mobilecore.nextRoutingMode(_state.value.routingMode)
            _state.value = _state.value.copy(routingMode = next)
            return next
        }
    }

    /** Adopt a mode word reported by the running core (clash mode stream). */
    fun setRoutingMode(value: String) = apply { it.copy(routingMode = value.trim()) }

    /**
     * Selected node and its latency-test status. Selection is a separate
     * fact from reachability — the TUI's filled dot is not a connectivity
     * claim — so this records both and derives the status word via
     * Mobilecore.nodeStatus (Untested / Testing / Reachable / Failed).
     */
    fun setSelectedNode(tag: String, delayMS: Int) = apply {
        val next = it.copy(selectedNode = tag, selectedNodeDelay = delayMS)
        next.copy(selectedNodeStatus = Mobilecore.nodeStatus(delayMS, next.selectedNodeTesting))
    }

    fun setNodeTesting(testing: Boolean) = apply {
        val next = it.copy(selectedNodeTesting = testing)
        next.copy(selectedNodeStatus = Mobilecore.nodeStatus(next.selectedNodeDelay, testing))
    }

    /** Feed one core config-state machine event (modified / regenerate_* / applied). */
    fun configEvent(event: String) {
        synchronized(lock) {
            val current = _state.value
            val next = try {
                Mobilecore.configStateTransition(current.configState, event)
            } catch (e: Exception) {
                _state.value = current.copy(notice = "config state machine: ${e.message}")
                return
            }
            _state.value = current.copy(configState = next)
        }
    }

    fun setNotice(message: String) = apply { it.copy(notice = message) }
    fun clearNotice() = apply { it.copy(notice = null) }
}
