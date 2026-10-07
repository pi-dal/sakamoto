package com.pidal.sakamoto

import com.pidal.sakamoto.ui.EditingRules
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

/** Structured predicate handling and safe JSON preservation of EditingRules. */
class EditingRulesTest {
    private val saved = """{"domain":["ads.example.com"],"port":[80,443],"inbound":["tun-in"],
        "action":"route","outbound":"proxy"}"""

    private fun strings(array: org.json.JSONArray): List<String> =
        (0 until array.length()).map { array.getString(it) }

    @Test fun editTouchesOnlyTheEditedPredicateAndActionLeaves() {
        val original = JSONObject(saved)
        val next = EditingRules.buildRule(original, "domain", "domain", " ads.example.com , tracker.example.net ", "route", "proxy")
        assertEquals(listOf("ads.example.com", "tracker.example.net"), strings(next.getJSONArray("domain")))
        // Unrelated predicates and metadata survive the edit verbatim.
        assertEquals(80, next.getJSONArray("port").getInt(0))
        assertEquals("tun-in", next.getJSONArray("inbound").getString(0))
        assertEquals("route", next.getString("action"))
        assertEquals("proxy", next.getString("outbound"))
        // The original rule object is never mutated.
        assertEquals(listOf("ads.example.com"), strings(original.getJSONArray("domain")))
        assertEquals("proxy", original.getString("outbound"))
    }

    @Test fun switchingPredicateRemovesOnlyTheOldKey() {
        val next = EditingRules.buildRule(JSONObject(saved), "domain", "domain_suffix", "example.org", "route", "proxy")
        assertFalse(next.has("domain"))
        assertEquals("example.org", next.getJSONArray("domain_suffix").getString(0))
        assertEquals(443, next.getJSONArray("port").getInt(1))
    }

    @Test fun structuredPredicateShapes() {
        val privateIp = EditingRules.buildRule(JSONObject(), null, "ip_is_private", "true", "route", "direct")
        assertTrue(privateIp.getBoolean("ip_is_private"))
        assertThrows(IllegalArgumentException::class.java) {
            EditingRules.buildRule(JSONObject(), null, "ip_is_private", "true, false", "route", "direct")
        }
        assertThrows(IllegalArgumentException::class.java) {
            EditingRules.buildRule(JSONObject(), null, "ip_is_private", "maybe", "route", "direct")
        }

        val mode = EditingRules.buildRule(JSONObject(), null, "clash_mode", "Global", "route", "direct")
        assertEquals("Global", mode.getString("clash_mode"))
        assertThrows(IllegalArgumentException::class.java) {
            EditingRules.buildRule(JSONObject(), null, "clash_mode", "Direct, Global", "route", "direct")
        }

        val ports = EditingRules.buildRule(JSONObject(), null, "port", "53, 443", "route", "proxy")
        assertEquals(53, ports.getJSONArray("port").getInt(0))
        assertEquals(443, ports.getJSONArray("port").getInt(1))
        assertThrows(IllegalArgumentException::class.java) { EditingRules.buildRule(JSONObject(), null, "port", "0", "route", "proxy") }
        assertThrows(IllegalArgumentException::class.java) { EditingRules.buildRule(JSONObject(), null, "port", "70000", "route", "proxy") }
        assertThrows(IllegalArgumentException::class.java) { EditingRules.buildRule(JSONObject(), null, "port", "eight", "route", "proxy") }

        val network = EditingRules.buildRule(JSONObject(), null, "network", "tcp, udp", "route", "proxy")
        assertEquals("udp", network.getJSONArray("network").getString(1))
        assertThrows(IllegalArgumentException::class.java) { EditingRules.buildRule(JSONObject(), null, "network", "icmp", "route", "proxy") }
    }

    @Test fun switchingAwayLeavesTheSlotEmptyInsteadOfStale() {
        val next = EditingRules.buildRule(JSONObject(saved), "domain", "ip_is_private", "true", "route", "proxy")
        assertFalse(next.has("domain"))
        assertTrue(next.getBoolean("ip_is_private"))
        assertEquals("proxy", next.getString("outbound"))
    }

    @Test fun matchlessActionsDropOutboundAndKeepOldPredicates() {
        val next = EditingRules.buildRule(JSONObject(saved), "domain", "domain", "", "sniff", "")
        assertEquals("sniff", next.getString("action"))
        assertFalse(next.has("outbound"))
        assertFalse(next.has("domain"))
        assertEquals(80, next.getJSONArray("port").getInt(0))
        assertEquals("tun-in", next.getJSONArray("inbound").getString(0))
    }

    @Test fun routeActionRequiresAnOutbound() {
        assertThrows(IllegalArgumentException::class.java) {
            EditingRules.buildRule(JSONObject(), null, "domain", "example.org", "route", " ")
        }
    }

    @Test fun unknownPredicateOrActionRejected() {
        assertThrows(IllegalArgumentException::class.java) {
            EditingRules.buildRule(JSONObject(), null, "geoip", "x", "route", "proxy")
        }
        assertThrows(IllegalArgumentException::class.java) {
            EditingRules.buildRule(JSONObject(), null, "domain", "x", "route-options", "proxy")
        }
    }

    @Test fun noOpSavePreservesScalarPredicateAndImplicitRouteAction() {
        val original = JSONObject("""{"domain":"example.org","outbound":"proxy","invert":false}""")
        val next = EditingRules.buildRule(original, "domain", "domain", EditingRules.readValues(original, "domain"), "route", "proxy")
        assertEquals(original.toString(), next.toString())
        assertTrue(next.get("domain") is String)
        assertFalse(next.has("action"))
    }

    @Test fun existingLogicalMatchDoesNotNeedAFabricatedDomain() {
        val original = JSONObject("""{"type":"logical","mode":"and","rules":[{"port":[443]}],"action":"route","outbound":"proxy"}""")
        val next = EditingRules.buildRule(original, null, "domain", "", "route", "proxy")
        assertEquals(original.toString(), next.toString())
        assertFalse(next.has("domain"))
    }

    @Test fun actionOptionsAreNotMistakenForAMatch() {
        assertThrows(IllegalArgumentException::class.java) {
            EditingRules.buildRule(JSONObject("""{"action":"route","outbound":"proxy","strategy":"prefer_ipv4"}"""),
                null, "domain", "", "route", "proxy")
        }
    }

    @Test fun entriesIgnoreBlankFragments() {
        assertEquals(listOf("a.com", "b.com"), EditingRules.entries(" a.com , , b.com "))
        assertEquals(emptyList<String>(), EditingRules.entries(" , "))
    }
}
