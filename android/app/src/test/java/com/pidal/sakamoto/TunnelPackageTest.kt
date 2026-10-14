package com.pidal.sakamoto

import com.pidal.sakamoto.runtime.TunnelPackage
import java.io.ByteArrayInputStream
import java.nio.file.Files
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class TunnelPackageTest {
    private fun zip(entries: List<Pair<String, String>>): ByteArray {
        val output = java.io.ByteArrayOutputStream()
        ZipOutputStream(output).use { zip -> entries.forEach { (name, text) -> zip.putNextEntry(ZipEntry(name)); zip.write(text.toByteArray()); zip.closeEntry() } }
        return output.toByteArray()
    }
    @Test fun rejectsPathTraversalBeforeWritingOutsideStaging() {
        val folder = Files.createTempDirectory("package-test").toFile()
        try {
            assertThrows(IllegalArgumentException::class.java) { TunnelPackage.extract(ByteArrayInputStream(zip(listOf("../escape" to "x"))), folder) }
            assertFalse(TunnelPackage.validPath("/absolute"))
            assertFalse(TunnelPackage.validPath("rules/../../escape"))
            assertTrue(TunnelPackage.validPath("imports/local/sources/main.conf"))
        } finally { folder.deleteRecursively() }
    }
    @Test fun updatedPackageKeepsLocalExclusionsAndIncludesNewRealityNodes() {
        val folder = Files.createTempDirectory("package-test").toFile()
        try {
            val old = """{"outbounds":[{"type":"urltest","tag":"RealityAuto","outbounds":["R1"]},{"type":"vless","tag":"R1","tls":{"reality":{"enabled":true}}},{"type":"vless","tag":"R2","tls":{"reality":{"enabled":true}}}]}"""
            val saved = com.pidal.sakamoto.runtime.ProxyGroupEditing.forImport(old, emptyList(), emptyMap())
            val updated = JSONObject(old)
            updated.getJSONArray("outbounds").put(JSONObject("""{"type":"vless","tag":"R3","tls":{"reality":{"enabled":true}}}"""))
            updated.getJSONArray("outbounds").getJSONObject(0).put("outbounds", org.json.JSONArray(listOf("R1", "R2", "R3")))
            val state = JSONObject().put("generatedContent", updated.toString()).toString()
            TunnelPackage.extract(ByteArrayInputStream(zip(listOf("sakamoto-config.json" to state))), folder)
            val prepared = TunnelPackage.prepare(folder, folder, saved)
            assertEquals(listOf("R1", "R3"), com.pidal.sakamoto.runtime.ProxyGroupEditing.groups(prepared.generated).single().members)
            val persisted = com.pidal.sakamoto.runtime.ProxyGroupEditing.decodeSaved(prepared.state.getJSONArray("automaticGroups"))
            assertEquals(listOf("R2"), persisted.single().excludedMembers)
            assertTrue(com.pidal.sakamoto.runtime.ProxyGroupEditing.rows(prepared.generated).any { it.getString("tag") == "R2" })
        } finally { folder.deleteRecursively() }
    }

    @Test fun rebindsRulePathsAndRequiresCompleteRules() {
        val folder = Files.createTempDirectory("package-test").toFile()
        try {
            val generated = """{"route":{"rule_set":[{"type":"local","tag":"rs","path":"/old/phone/files/rules/proxy.srs"}]}}"""
            val state = JSONObject().put("generatedContent", generated).toString()
            TunnelPackage.extract(ByteArrayInputStream(zip(listOf("sakamoto-config.json" to state, "rules/proxy.srs" to "synthetic"))), folder)
            val prepared = TunnelPackage.prepare(folder, folder)
            val path = JSONObject(prepared.generated).getJSONObject("route").getJSONArray("rule_set").getJSONObject(0).getString("path")
            assertEquals(java.io.File(folder, "rules/proxy.srs").absolutePath, path)
            java.io.File(folder, "rules/proxy.srs").delete()
            assertThrows(IllegalArgumentException::class.java) { TunnelPackage.prepare(folder, folder) }
        } finally { folder.deleteRecursively() }
    }
}
