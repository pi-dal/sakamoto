package com.pidal.sakamoto

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pure-model tests for the built-in Tailscale integration. These run on the
 * JVM (`./gradlew test`) without libbox, Android or any tailnet — the libbox
 * glue is a separate file (runtime/TailscaleBinding.kt) precisely so this
 * layer stays testable.
 */
class TailscaleModelsTest {

    // --- Backend state ------------------------------------------------------

    @Test
    fun backendStateParsesAllWireStrings() {
        assertEquals(TailscaleBackendState.Stopped, TailscaleBackendState.parse("Stopped"))
        assertEquals(TailscaleBackendState.Starting, TailscaleBackendState.parse("Starting"))
        assertEquals(TailscaleBackendState.NeedsLogin, TailscaleBackendState.parse("NeedsLogin"))
        assertEquals(TailscaleBackendState.NeedsMachineAuth, TailscaleBackendState.parse("NeedsMachineAuth"))
        assertEquals(TailscaleBackendState.Running, TailscaleBackendState.parse("Running"))
    }

    @Test
    fun backendStateCarriesUnrecognizedVerbatim() {
        val unknown = TailscaleBackendState.parse("NeedsDCUpgrade")
        assertEquals(TailscaleBackendState.Unrecognized("NeedsDCUpgrade"), unknown)
        assertEquals("NeedsDCUpgrade", unknown.wireString)
        // Whitespace-tolerant parse, verbatim carry of the ORIGINAL raw string.
        assertEquals(TailscaleBackendState.Unrecognized(" SomeFutureState "),
            TailscaleBackendState.parse(" SomeFutureState "))
    }

    @Test
    fun backendStateRoundTrips() {
        for (wire in listOf("Stopped", "Starting", "NeedsLogin", "NeedsMachineAuth", "Running")) {
            assertEquals(wire, TailscaleBackendState.parse(wire).wireString)
        }
    }

    @Test
    fun backendStatePredicates() {
        assertTrue(TailscaleBackendState.parse("Running").running)
        assertFalse(TailscaleBackendState.parse("Starting").running)
        assertTrue(TailscaleBackendState.parse("NeedsLogin").needsLoginFlow)
        assertTrue(TailscaleBackendState.parse("NeedsMachineAuth").needsLoginFlow)
        assertFalse(TailscaleBackendState.parse("Running").needsLoginFlow)
        assertFalse(TailscaleBackendState.parse("Stopped").needsLoginFlow)
    }

    // --- Peer / endpoint summaries -------------------------------------------

    private fun peer(
        id: String,
        name: String,
        exitOption: Boolean = false,
        exitNode: Boolean = false,
        online: Boolean = false,
    ) = TailscalePeerSummary(
        stableID = id,
        hostName = name,
        dnsName = "$name.tail-scale.ts.net.",
        os = "android",
        tailscaleIPs = listOf("100.64.0.$id"),
        online = online,
        active = false,
        expired = false,
        exitNode = exitNode,
        exitNodeOption = exitOption,
        lastSeenUnixSeconds = 0,
    )

    private fun endpoint(
        self: TailscalePeerSummary,
        currentExit: TailscalePeerSummary?,
        peers: List<TailscalePeerSummary>,
    ) = TailscaleEndpointSummary(
        endpointTag = "ts",
        backendState = TailscaleBackendState.Running,
        networkName = "example.ts.net",
        magicDNSSuffix = "ts.net",
        authURL = "",
        selfPeer = self,
        exitNodePeer = currentExit,
        peers = peers,
    )

    @Test
    fun exitNodeCandidatesExcludeSelfAndCurrentExit() {
        val self = peer("1", "phone")
        val current = peer("2", "box", exitOption = true, exitNode = true)
        val candidateA = peer("3", "vps-a", exitOption = true)
        val candidateB = peer("4", "vps-b", exitOption = true)
        val plain = peer("5", "laptop", exitOption = false)
        val endpoint = endpoint(self, current, listOf(self, current, candidateA, candidateB, plain))
        assertEquals(listOf(candidateA, candidateB), endpoint.exitNodeCandidates)
    }

    @Test
    fun exitNodeCandidatesEmptyWithoutOptions() {
        val self = peer("1", "phone")
        val endpoint = endpoint(self, null, listOf(self, peer("2", "laptop")))
        assertTrue(endpoint.exitNodeCandidates.isEmpty())
    }

    @Test
    fun peerDisplayNamePrefersDNSLabel() {
        assertEquals("vps-a", peer("3", "vps-a").displayName)
        val noDns = peer("6", "bare").copy(dnsName = "")
        assertEquals("bare", noDns.displayName)
    }

    @Test
    fun exitNodePingTargetPrefersTailscaleIP() {
        val exit = peer("2", "box", exitOption = true, exitNode = true)
        val endpoint = endpoint(peer("1", "phone"), exit, listOf(exit))
        assertEquals("100.64.0.2", endpoint.exitNodePingTarget)
        val noIP = endpoint.copy(exitNodePeer = exit.copy(tailscaleIPs = emptyList()))
        assertEquals(noIP.exitNodePeer?.dnsName, noIP.exitNodePingTarget)
        val none = endpoint.copy(exitNodePeer = null)
        assertEquals(null, none.exitNodePingTarget)
    }

    // --- Capabilities ---------------------------------------------------------

    @Test
    fun unsupportedListNamesTheOmittedFeatures() {
        val capabilities = TailscaleCapabilities.unsupported.map { it.capability }
        assertEquals(
            listOf("taildrop", "tailscale-ssh", "serve", "login-with-auth-key-ui", "external-cli-probe"),
            capabilities,
        )
        // Every reason is non-empty — the UI renders them verbatim.
        assertTrue(TailscaleCapabilities.unsupported.all { it.reason.isNotBlank() })
        // The supported actions are exactly the four RPCs + observe.
        assertEquals(
            listOf("observe-status", "set-exit-node", "clear-exit-node-selection", "logout", "ping-peer"),
            TailscaleCapabilities.supportedActions,
        )
    }

    // --- Masking & ping rendering ---------------------------------------------

    @Test
    fun authKeyMaskingNeverShowsTheFullKey() {
        val masked = TailscaleAuthKeyMasking.mask("tskey-auth-k1234567890abcdef-Klub")
        assertTrue(masked.startsWith("tskey-auth-"))
        assertTrue(masked.endsWith("••••••"))
        assertFalse(masked.contains("Klub"))
        assertEquals("••••••", TailscaleAuthKeyMasking.mask("short"))
        assertEquals("", TailscaleAuthKeyMasking.mask(""))
        // Masking is not reversible: no suffix of the input beyond the prefix survives.
        val input = "tskey-auth-abcdefghijklmnop"
        val out = TailscaleAuthKeyMasking.mask(input)
        assertTrue(!out.contains("lmnop"))
        assertEquals(input.take(12) + "••••••", out)
    }

    @Test
    fun pingOutcomeRendersWithoutSecrets() {
        val direct = TailscalePingOutcome(
            latencyMs = 12.5, direct = true, endpoint = "100.64.0.2:41641",
            peerRelay = "", derpRegionCode = "", error = "",
        )
        assertEquals("ping ok: 12.5ms direct via 100.64.0.2:41641", direct.render())
        val relay = TailscalePingOutcome(
            latencyMs = 40.0, direct = false, endpoint = "",
            peerRelay = "derp-tok", derpRegionCode = "tok", error = "",
        )
        assertTrue(relay.render().contains("DERP tok"))
        assertTrue(relay.render().contains("derp-tok"))
        val failed = TailscalePingOutcome(
            latencyMs = 0.0, direct = false, endpoint = "", peerRelay = "",
            derpRegionCode = "", error = "timeout",
        )
        assertEquals("ping failed: timeout", failed.render())
    }
}
