package com.pidal.sakamoto.runtime

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import java.util.Base64

class NodeLinkEditingTest {
    @Test fun editsIPv6UnicodeRemarkAndPortWithoutChangingCredentialOrOptions() {
        val raw = "vless://uuid%2Bsecret@old.example:443?security=reality&type=ws&path=%2Fa%3Fb%3D1&sni=cdn.example#Old"
        val saved = NodeLinkEditing.update(raw, "Japan #1 / 日本", "2001:db8::8", 8443)
        assertEquals(NodeLinkEditing.Endpoint("Japan #1 / 日本", "2001:db8::8", 8443), NodeLinkEditing.endpoint(saved))
        assertEquals(java.net.URI(raw).rawQuery, java.net.URI(saved).rawQuery)
        assertEquals(java.net.URI(raw).rawUserInfo, java.net.URI(saved).rawUserInfo)
    }
    @Test fun editsShadowrocketAuthorityAndPreservesRangedPorts() {
        val authority = Base64.getEncoder().encodeToString("user:secret@old.example:1080".toByteArray())
        val saved = NodeLinkEditing.update("socks://$authority?remarks=Old&chain=PROXY", "New", "192.0.2.8", 1081)
        assertEquals(NodeLinkEditing.Endpoint("New", "192.0.2.8", 1081), NodeLinkEditing.endpoint(saved))
        assertTrue(saved.contains("chain=PROXY"))
        val raw = "hy2://secret@old.example?mport=443-445&sni=cdn.example#Old"
        assertNull(NodeLinkEditing.endpoint(raw).port)
        assertTrue(NodeLinkEditing.update(raw, "New", "192.0.2.9", null).contains("mport=443-445"))
    }
    @Test fun editsVMessWhilePreservingUnknownFields() {
        val raw = "vmess://" + Base64.getEncoder().encodeToString("""{"ps":"Old","add":"old.example","port":"443","id":"uuid","path":"/keep","future":{"keep":true}}""".toByteArray())
        val saved = NodeLinkEditing.update(raw, "New", "192.0.2.10", 8443)
        val root = JSONObject(String(Base64.getDecoder().decode(saved.substring(8))))
        assertEquals("/keep", root.getString("path")); assertTrue(root.getJSONObject("future").getBoolean("keep"))
        assertEquals(NodeLinkEditing.Endpoint("New", "192.0.2.10", 8443), NodeLinkEditing.endpoint(saved))
    }
    @Test fun rejectsInvalidEndpointFields() {
        for ((name, server, port) in listOf(Triple("", "x", 443), Triple("X", "https://x", 443), Triple("X", "x", 65536))) {
            try { NodeLinkEditing.update("socks://u:p@x:1080#Old", name, server, port); fail("Accepted invalid fields") } catch (_: IllegalArgumentException) {}
        }
    }
    @Test fun renameRepairsNestedRoutingGroupsAndDNSWithoutChangingOtherValues() {
        val raw = """{"outbounds":[{"tag":"A","type":"socks","server":"A","server_port":1080,"password":"A"},{"tag":"Group","type":"selector","outbounds":["A"],"default":"A"},{"tag":"Next","type":"socks","detour":"A"}],"route":{"final":"A","rules":[{"type":"logical","rules":[{"domain":["A"],"outbound":"A"}]}]},"dns":{"servers":[{"tag":"A","server":"A","detour":"A"}]}}"""
        val root = JSONObject(ConfigEdits.renamedOutbound(raw, "A", "Japan"))
        val nodes = root.getJSONArray("outbounds")
        assertEquals("Japan", nodes.getJSONObject(0).getString("tag")); assertEquals("A", nodes.getJSONObject(0).getString("password"))
        assertEquals("Japan", nodes.getJSONObject(1).getString("default")); assertEquals("Japan", nodes.getJSONObject(2).getString("detour"))
        assertEquals("Japan", root.getJSONObject("route").getString("final"))
        val nested = root.getJSONObject("route").getJSONArray("rules").getJSONObject(0).getJSONArray("rules").getJSONObject(0)
        assertEquals("Japan", nested.getString("outbound")); assertEquals("A", nested.getJSONArray("domain").getString(0))
        val dns = root.getJSONObject("dns").getJSONArray("servers").getJSONObject(0)
        assertEquals("Japan", dns.getString("detour")); assertEquals("A", dns.getString("tag")); assertEquals("A", dns.getString("server"))
        try { ConfigEdits.renamedOutbound(raw, "A", "Group"); fail("Allowed duplicate name") } catch (_: IllegalArgumentException) {}
    }
}
