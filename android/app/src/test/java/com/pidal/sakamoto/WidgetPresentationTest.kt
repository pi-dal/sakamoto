package com.pidal.sakamoto

import com.pidal.sakamoto.runtime.ConfigRepository.Group
import com.pidal.sakamoto.runtime.ConfigRepository.GroupItem
import com.pidal.sakamoto.runtime.RuntimeState
import com.pidal.sakamoto.runtime.WidgetPresentation
import org.junit.Assert.*
import org.junit.Test

class WidgetPresentationTest {
    private val groups = listOf(Group("MainProxy", "selector", true, "RealityAuto", emptyList()),
        Group("RealityAuto", "urltest", false, "node-a", listOf(GroupItem("node-a", "vless", 128, 1000))))
    private val running = RuntimeState(serviceState = "Running", routingMode = "Rule")
    @Test fun resolvesGroupToActualNodeWithoutClaimingVpnReachable() {
        val result = WidgetPresentation.resolve(running, true, true, groups, 1010)
        assertEquals("node-a", result.node)
        assertEquals(128, result.delay)
        assertEquals("vless", result.protocol)
        assertEquals("MainProxy", result.group)
        assertEquals(WidgetPresentation.Latency.FRESH, result.latency)
        assertEquals(WidgetPresentation.Health.RUNNING, result.health)
    }
    @Test fun staleAndDisconnectedMeasurementsAreNotRealtime() {
        assertEquals(WidgetPresentation.Latency.STALE, WidgetPresentation.resolve(running, true, true, groups, 1300).latency)
        val off = WidgetPresentation.resolve(RuntimeState(), false, true, groups, 1010)
        assertEquals("", off.node)
        assertEquals("", off.protocol)
        assertEquals("", off.group)
        assertEquals(WidgetPresentation.Latency.NONE, off.latency)
    }
    @Test fun reportsFailureUntestedNoNetworkAndUnconfirmedSeparately() {
        val failed = groups.map { if (it.tag == "RealityAuto") it.copy(items = listOf(GroupItem("node-a", "vless", 0, 1000))) else it }
        assertEquals(WidgetPresentation.Latency.FAILED, WidgetPresentation.resolve(running, true, true, failed, 1010).latency)
        val untested = groups.map { if (it.tag == "RealityAuto") it.copy(items = listOf(GroupItem("node-a", "vless", 0, 0))) else it }
        assertEquals(WidgetPresentation.Latency.UNTESTED, WidgetPresentation.resolve(running, true, true, untested, 1010).latency)
        assertEquals(WidgetPresentation.Health.NO_NETWORK, WidgetPresentation.resolve(running.copy(probeState = "Reachable"), true, false, groups, 1010).health)
        assertEquals(WidgetPresentation.Health.UNCONFIRMED, WidgetPresentation.resolve(running, false, true, groups, 1010).health)
    }
    @Test fun doesNotGuessSelectionFromFastestResult() {
        val unknown = groups.map { if (it.tag == "RealityAuto") it.copy(selected = "") else it }
        val result = WidgetPresentation.resolve(running, true, true, unknown, 1010)
        assertEquals("RealityAuto", result.node)
        assertEquals(WidgetPresentation.Latency.UNTESTED, result.latency)
    }
    @Test fun cyclicGroupsTerminateWithoutInventingLatency() {
        val cyclic = listOf(Group("A", "selector", true, "B", emptyList()), Group("B", "selector", true, "A", emptyList()))
        assertEquals(WidgetPresentation.Latency.UNTESTED, WidgetPresentation.resolve(running, true, true, cyclic, 1010).latency)
    }
}
