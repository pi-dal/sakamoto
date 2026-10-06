package com.pidal.sakamoto

import com.pidal.sakamoto.runtime.ConfigRepository.Group
import com.pidal.sakamoto.runtime.ConfigRepository.GroupItem
import com.pidal.sakamoto.runtime.FallbackDecision
import org.junit.Assert.*
import org.junit.Test

class FallbackDecisionTest {
    private val chain = listOf("preferred", "fallback")
    @Test fun requiresFreshResultAndKeepsManualChoice() {
        val groups = listOf(Group("MainProxy", "selector", true, "preferred", emptyList()),
            Group("preferred", "urltest", false, "", listOf(GroupItem("old", "vless", 10, 99))),
            Group("fallback", "urltest", false, "", listOf(GroupItem("fresh", "vless", 80, 101))))
        assertEquals("fallback", FallbackDecision.candidate(groups, "MainProxy", chain, 100))
        assertNull(FallbackDecision.candidate(groups.map { if (it.tag == "MainProxy") it.copy(selected = "ManualPick") else it }, "MainProxy", chain, 100))
        assertNull(FallbackDecision.candidate(groups, "MainProxy", chain, 200))
    }
    @Test fun respectsPriorityRatherThanLowestLatency() {
        val groups = listOf(Group("MainProxy", "selector", true, "fallback", emptyList()),
            Group("preferred", "urltest", false, "", listOf(GroupItem("preferred-node", "vless", 200, 101))),
            Group("fallback", "urltest", false, "", listOf(GroupItem("fallback-node", "vless", 10, 101))))
        assertEquals("preferred", FallbackDecision.candidate(groups, "MainProxy", chain, 100))
    }
}
