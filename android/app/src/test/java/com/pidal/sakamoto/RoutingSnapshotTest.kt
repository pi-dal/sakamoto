package com.pidal.sakamoto

import com.pidal.sakamoto.runtime.RoutingSnapshot
import org.junit.Assert.*
import org.junit.Test

class RoutingSnapshotTest {
    @Test fun keepsRulePriorityAndReportsActualExitEvenWithoutSidecarExit() {
        val result = RoutingSnapshot.parse("""{
          "outbounds":[
            {"type":"selector","tag":"MainProxy","outbounds":["entry"]},
            {"type":"vless","tag":"entry","uuid":"never-display"},
            {"type":"socks","tag":"exit","server":"exit.example","server_port":1080,"username":"secret-user","password":"secret-password","detour":"MainProxy"},
            {"type":"socks","tag":"standalone","server":"other.example","server_port":1080}
          ],
          "route":{"rules":[
            {"action":"reject","rule_set":["blocked"]},
            {"action":"route","clash_mode":"Global","outbound":"exit"},
            {"action":"route","rule_set":["proxy"],"outbound":"exit"}
          ],"final":"direct"}
        }""")
        assertEquals(listOf("reject", "route", "route"), result.rules.map { it.action })
        assertEquals(listOf(1, 2, 3), result.rules.map { it.order })
        assertEquals("clash_mode: Global", result.rules[1].match)
        assertEquals("exit", result.rules[2].outbound)
        assertEquals("direct", result.final)
        assertEquals(1, result.exits.size)
        assertEquals("MainProxy", result.exits.single().detour)
        assertTrue(result.exits.single().authenticated)
        assertFalse(result.toString().contains("secret-user"))
        assertFalse(result.toString().contains("secret-password"))
        assertFalse(result.toString().contains("never-display"))
    }

    @Test fun preservesLogicalRulesAndDoesNotInventExitOrPolicy() {
        val result = RoutingSnapshot.parse("""{"route":{"rules":[{"type":"logical","mode":"or","rules":[{"domain":["example.com"]},{"domain_suffix":["example.org"]}],"action":"route","outbound":"direct"}]}}""")
        assertTrue(result.exits.isEmpty())
        assertTrue(result.rules.single().match.contains("domain: example.com"))
        assertTrue(result.rules.single().match.contains("domain_suffix: example.org"))
        val empty = RoutingSnapshot.parse("{}")
        assertTrue(empty.rules.isEmpty())
        assertTrue(empty.exits.isEmpty())
    }
}
