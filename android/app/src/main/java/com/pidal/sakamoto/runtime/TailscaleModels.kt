package com.pidal.sakamoto.runtime

/**
 * Pure Tailscale state model for the built-in sing-box Tailscale endpoint —
 * the Kotlin mirror of ios/Sources/SakamotoKit/TailscaleVocabulary.swift,
 * kept free of ANY libbox import so it is unit-testable without the AAR
 * (the libbox binding glue lives in TailscaleBinding.kt / CommandClientRuntime).
 *
 * Two different things are often called "Tailscale support"; the same split
 * as iOS applies:
 *
 *   1. BUILT-IN Tailscale client (what this file models): the sing-box
 *      Tailscale endpoint embeds the Tailscale client (tsnet userspace
 *      networking) inside the tunnel process. Login state, tailnet name,
 *      peers and exit-node selection are real tailnet facts surfaced through
 *      the libbox command channel.
 *   2. EXTERNAL Tailscale detection (deliberately NOT implemented): probing
 *      a `tailscale` binary on the device. Android has no system tailscaled
 *      for an app to query; pretending otherwise would be fake support.
 *
 * Wire facts are libbox v1.14.2 (experimental/libbox/command_types_tailscale.go):
 * BackendState values are tailscale ipn.State strings ("Stopped", "Starting",
 * "NeedsLogin", "NeedsMachineAuth", "Running").
 */

/** Normalized tailscaled backend state. `Unrecognized` carries values from a
 * newer core verbatim — never guessed into a known bucket. */
sealed class TailscaleBackendState {
    data object Stopped : TailscaleBackendState()
    data object Starting : TailscaleBackendState()
    /** Login required; the login URL arrives separately (authURL). */
    data object NeedsLogin : TailscaleBackendState()
    /** Device authorization required (tailnet policy approval). */
    data object NeedsMachineAuth : TailscaleBackendState()
    data object Running : TailscaleBackendState()
    data class Unrecognized(val raw: String) : TailscaleBackendState()

    companion object {
        fun parse(backendState: String): TailscaleBackendState =
            when (backendState.trim()) {
                "Stopped" -> Stopped
                "Starting" -> Starting
                "NeedsLogin" -> NeedsLogin
                "NeedsMachineAuth" -> NeedsMachineAuth
                "Running" -> Running
                else -> Unrecognized(backendState)
            }
    }

    /** The exact wire string this state was parsed from (round-trip safe). */
    val wireString: String
        get() = when (this) {
            is Stopped -> "Stopped"
            is Starting -> "Starting"
            is NeedsLogin -> "NeedsLogin"
            is NeedsMachineAuth -> "NeedsMachineAuth"
            is Running -> "Running"
            is Unrecognized -> raw
        }

    /** Whether the tailnet session is usable (peers routable). */
    val running: Boolean get() = this is Running

    /** Whether a login flow must complete before anything else works. */
    val needsLoginFlow: Boolean get() = this is NeedsLogin || this is NeedsMachineAuth
}

/** One peer on the tailnet, shaped for the UI. Selection (exit node) is a
 * separate fact and never implies reachability (TUI rule: selected ≠
 * reachable). */
data class TailscalePeerSummary(
    val stableID: String,
    val hostName: String,
    val dnsName: String,
    val os: String,
    val tailscaleIPs: List<String>,
    val online: Boolean,
    val active: Boolean,
    val expired: Boolean,
    val exitNode: Boolean,
    val exitNodeOption: Boolean,
    val lastSeenUnixSeconds: Long,
) {
    val displayName: String
        get() = dnsName.substringBefore(".").ifEmpty { hostName }
}

/** Aggregate tailnet status for one configured Tailscale endpoint. */
data class TailscaleEndpointSummary(
    val endpointTag: String,
    val backendState: TailscaleBackendState,
    val networkName: String,
    val magicDNSSuffix: String,
    /** Non-empty exactly when the core waits for the browser login flow. */
    val authURL: String,
    val selfPeer: TailscalePeerSummary?,
    val exitNodePeer: TailscalePeerSummary?,
    val peers: List<TailscalePeerSummary>,
) {
    /** Exit-node candidates: peers advertising the option, excluding self and
     * the currently selected exit node. Empty = no candidates (the UI shows
     * the fact, it does not invent options). */
    val exitNodeCandidates: List<TailscalePeerSummary>
        get() {
            val selfID = selfPeer?.stableID
            val currentExitID = exitNodePeer?.stableID
            return peers.filter { peer ->
                peer.exitNodeOption && peer.stableID != selfID && peer.stableID != currentExitID
            }
        }

    /** A safe peer address for StartTailscalePing: the first Tailscale IP,
     * else the DNS name. Never a MagicDNS suffix guess. */
    val exitNodePingTarget: String?
        get() = exitNodePeer?.let { it.tailscaleIPs.firstOrNull() ?: it.dnsName.ifEmpty { null } }
}

/** One latency probe round (libbox TailscalePingResult). */
data class TailscalePingOutcome(
    val latencyMs: Double,
    val direct: Boolean,
    val endpoint: String,
    val peerRelay: String,
    val derpRegionCode: String,
    val error: String,
) {
    /** Display line. Carries no credentials by construction (peer IP, relay
     * region, latency are not secrets). */
    fun render(): String = when {
        error.isNotEmpty() -> "ping failed: $error"
        direct -> "ping ok: ${latencyMs}ms direct via $endpoint"
        else -> "ping ok: ${latencyMs}ms via DERP $derpRegionCode (relay $peerRelay)"
    }
}

/** What the built-in integration can and cannot do, with reasons. Data, not
 * UI copy: the UI renders these verbatim so a missing feature is always
 * explainable. The ts_omit_* tags are part of the v1.14.2 builder's
 * sharedTags for ALL platforms — Android included — so the omissions are
 * build facts, not iOS quirks. */
object TailscaleCapabilities {
    /** Actions wired to real libbox v1.14.2 CommandClient methods. */
    val supportedActions: List<String> = listOf(
        "observe-status", "set-exit-node", "clear-exit-node-selection", "logout", "ping-peer",
    )

    val unsupported: List<UnsupportedCapability> = listOf(
        UnsupportedCapability(
            capability = "taildrop",
            reason = "libbox v1.14.2 builds set ts_omit_taildrop; file sharing RPCs are absent",
        ),
        UnsupportedCapability(
            capability = "tailscale-ssh",
            reason = "libbox v1.14.2 builds set ts_omit_ssh; Tailscale SSH sessions are absent",
        ),
        UnsupportedCapability(
            capability = "serve",
            reason = "no libbox API exposes tailscale serve/funnel",
        ),
        UnsupportedCapability(
            capability = "login-with-auth-key-ui",
            reason = "auth keys are configuration input (Keystore -> endpoint config at start), " +
                "not a client RPC; the login flow is the auth URL",
        ),
        UnsupportedCapability(
            capability = "external-cli-probe",
            reason = "Android apps cannot query a system tailscaled; probing one would be fake support",
        ),
    )

    data class UnsupportedCapability(val capability: String, val reason: String)
}

/** Auth-key display masking: the key NEVER renders in full. */
object TailscaleAuthKeyMasking {
    private const val KEEP_PREFIX = 12

    fun mask(key: String): String {
        val trimmed = key.trim()
        if (trimmed.isEmpty()) return ""
        if (trimmed.length <= KEEP_PREFIX) return "••••••"
        return trimmed.take(KEEP_PREFIX) + "••••••"
    }
}
