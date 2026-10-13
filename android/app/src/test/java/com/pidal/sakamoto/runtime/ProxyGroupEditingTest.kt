package com.pidal.sakamoto.runtime

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class ProxyGroupEditingTest {
    private val content = """{"outbounds":[{"tag":"A","type":"trojan","server":"a.example","server_port":443,"password":"test"},{"tag":"B","type":"socks","server":"b.example","server_port":1080},{"tag":"Main","type":"selector","outbounds":["A","B"],"interrupt_exist_connections":true},{"tag":"direct","type":"direct"}],"endpoints":[{"tag":"tail","type":"tailscale"}],"route":{"final":"Main"},"future":{"keep":true}}"""
    private fun rejects(edit: ProxyGroupEdit, input: String = content) {
        try { ProxyGroupEditing.apply(input, edit); fail("Invalid edit was accepted") } catch (_: IllegalArgumentException) {}
    }
    @Test fun savesOrderedMembersAndDefaultAndPreservesUnknownFields() {
        val result = ProxyGroupEditing.apply(content, ProxyGroupEdit("Main", members = listOf("B", "tail", "A"), defaultMember = "B"))
        val group = ProxyGroupEditing.groups(result).single()
        assertEquals(listOf("B", "tail", "A"), group.members)
        assertEquals("B", group.defaultMember)
        assertTrue(JSONObject(result).getJSONObject("future").getBoolean("keep"))
        assertTrue(ProxyGroupEditing.rows(result).first { it.getString("tag") == "Main" }.getBoolean("interrupt_exist_connections"))
        assertEquals("test", ProxyGroupEditing.rows(result).first { it.getString("tag") == "A" }.getString("password"))
    }
    @Test fun addsAndIncludesNestedGroupThenRejectsIndirectCycle() {
        val added = ProxyGroupEditing.apply(content, ProxyGroupEdit("Custom", members = listOf("A", "B")))
        val nested = ProxyGroupEditing.apply(added, ProxyGroupEdit("Main", members = listOf("Custom")))
        assertEquals(listOf("Custom"), ProxyGroupEditing.groups(nested).first { it.tag == "Main" }.members)
        rejects(ProxyGroupEdit("Custom", members = listOf("Main")), nested)
    }
    @Test fun rejectsEmptyMissingDuplicateAndSelfMembers() {
        rejects(ProxyGroupEdit("Main"))
        rejects(ProxyGroupEdit("Main", members = listOf("unknown")))
        rejects(ProxyGroupEdit("Main", members = listOf("A", "A")))
        rejects(ProxyGroupEdit("Main", members = listOf("Main")))
        rejects(ProxyGroupEdit("Main", members = listOf("A"), defaultMember = "B"))
    }
    @Test fun rejectsNameCollisionsWithNodeOrEndpoint() {
        rejects(ProxyGroupEdit("A", members = listOf("B")))
        rejects(ProxyGroupEdit("tail", members = listOf("B")))
    }
    @Test fun convertsBothDirectionsWithoutKeepingIncompatibleSettings() {
        val auto = ProxyGroupEditing.apply(content, ProxyGroupEdit("Main", "urltest", listOf("B", "A"), interval = "30s"))
        assertEquals("30s", ProxyGroupEditing.groups(auto).single().interval)
        assertFalse(ProxyGroupEditing.rows(auto).first { it.getString("tag") == "Main" }.has("default"))
        val manual = ProxyGroupEditing.apply(auto, ProxyGroupEdit("Main", members = listOf("B", "A")))
        val row = ProxyGroupEditing.rows(manual).first { it.getString("tag") == "Main" }
        assertFalse(row.has("default"))
        assertEquals("", ProxyGroupEditing.groups(manual).single().defaultMember)
        assertFalse(row.has("url")); assertFalse(row.has("interval"))
    }
    @Test fun rejectsInvalidAutomaticTestSettings() {
        rejects(ProxyGroupEdit("Main", "urltest", listOf("A"), url = "ftp://a.example"))
        rejects(ProxyGroupEdit("Main", "urltest", listOf("A"), interval = "0s"))
        rejects(ProxyGroupEdit("Main", "urltest", listOf("A"), interval = "invalid"))
    }
    @Test fun rejectsCyclesThroughNodeDetours() {
        val root = JSONObject(content)
        root.getJSONArray("outbounds").getJSONObject(0).put("detour", "Main")
        rejects(ProxyGroupEdit("Main", members = listOf("A")), root.toString())
    }
}
