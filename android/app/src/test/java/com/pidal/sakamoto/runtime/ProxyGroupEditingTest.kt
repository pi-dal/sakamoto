package com.pidal.sakamoto.runtime

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class ProxyGroupEditingTest {
    private val content = """{"outbounds":[{"tag":"A","type":"trojan","server":"a.example","server_port":443,"password":"test"},{"tag":"B","type":"socks","server":"b.example","server_port":1080},{"tag":"Main","type":"selector","outbounds":["A","B"],"interrupt_exist_connections":true},{"tag":"direct","type":"direct"}],"endpoints":[{"tag":"tail","type":"tailscale"}],"route":{"final":"Main"},"future":{"keep":true}}"""
    private val automaticContent = """{"outbounds":[{"tag":"R1","type":"vless","tls":{"reality":{"enabled":true}}},{"tag":"R2","type":"vless","tls":{"reality":{"enabled":true}}},{"tag":"Other","type":"trojan"},{"tag":"RealityAuto","type":"urltest","outbounds":["R1","R2"],"url":"https://test.example/204","interval":"1m"},{"tag":"ManualPick","type":"selector","outbounds":["R1","R2","Other"]}]}"""
    @Test fun newRealityNodesJoinButExclusionsSurvivePersistenceAbsenceAndRestoration() {
        val original = ProxyGroupEditing.groups(automaticContent).first { it.tag == "RealityAuto" }
        val excluded = ProxyGroupEditing.recording(automaticContent, original.copy(members = listOf("R1")))
        assertEquals(listOf("R2"), excluded.excludedMembers)
        val restored = ProxyGroupEditing.decodeSaved(ProxyGroupEditing.encodeSaved(listOf(excluded))).single()
        val root = JSONObject(automaticContent)
        root.getJSONArray("outbounds").put(JSONObject("""{"tag":"R3","type":"vless","tls":{"reality":{"enabled":true}}}"""))
        val updated = ProxyGroupEditing.applySaved(root.toString(), listOf(restored))
        assertEquals(listOf("R1", "R3"), ProxyGroupEditing.groups(updated).first { it.tag == "RealityAuto" }.members)
        assertEquals(listOf("R1", "R2", "Other"), ProxyGroupEditing.groups(updated).first { it.tag == "ManualPick" }.members)
        val absent = JSONObject(updated)
        val rows = absent.getJSONArray("outbounds")
        for (index in rows.length() - 1 downTo 0) if (rows.getJSONObject(index).optString("tag") == "R2") rows.remove(index)
        val savedAgain = ProxyGroupEditing.recording(absent.toString(), ProxyGroupEditing.groups(absent.toString()).first { it.tag == "RealityAuto" }, restored)
        assertEquals(listOf("R2"), savedAgain.excludedMembers)
        assertEquals(listOf("R1", "R3"), ProxyGroupEditing.groups(ProxyGroupEditing.applySaved(root.toString(), listOf(savedAgain))).first { it.tag == "RealityAuto" }.members)
        val readded = ProxyGroupEditing.recording(root.toString(), restored.copy(members = listOf("R1", "R2", "R3")), savedAgain)
        assertEquals(emptyList<String>(), readded.excludedMembers)
    }
    @Test fun migrationRemembersExclusionsAcrossLocalRenameAndPackageImport() {
        val removed = ProxyGroupEditing.apply(automaticContent, ProxyGroupEditing.groups(automaticContent).first { it.tag == "RealityAuto" }.copy(members = listOf("R1")))
        val legacy = ProxyGroupEditing.forImport(removed, emptyList(), emptyMap()).single()
        assertEquals(listOf("R2"), legacy.excludedMembers)
        val renamed = legacy.copy(excludedMembers = listOf("Japan"))
        val imported = ProxyGroupEditing.forImport(removed, listOf(renamed), mapOf("R2" to "Japan")).single()
        assertEquals(listOf("R2"), imported.excludedMembers)
        assertEquals(listOf("R1"), ProxyGroupEditing.groups(ProxyGroupEditing.applySaved(automaticContent, listOf(imported))).first { it.tag == "RealityAuto" }.members)
    }
    @Test fun emptyAutomaticGroupIsRejectedWithoutChangingManualMembership() {
        val group = ProxyGroupEditing.groups(automaticContent).first { it.tag == "RealityAuto" }
        val edit = ProxyGroupEditing.recording(automaticContent, group.copy(members = emptyList()))
        assertThrows(IllegalArgumentException::class.java) { ProxyGroupEditing.applySaved(automaticContent, listOf(edit)) }
        assertEquals(listOf("R1", "R2", "Other"), ProxyGroupEditing.groups(automaticContent).first { it.tag == "ManualPick" }.members)
    }

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
