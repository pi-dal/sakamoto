package com.pidal.sakamoto

import com.pidal.sakamoto.ui.EditingProtocols
import com.pidal.sakamoto.ui.EditingProtocols.FieldSpec
import com.pidal.sakamoto.ui.EditingProtocols.Kind
import com.pidal.sakamoto.ui.EditingProtocols.Section
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class EditingProtocolsTest {
    private val vless = """{"tag":"node","type":"vless","server":"example.org","server_port":443,
        "uuid":"6ea4c28f-63a9-45b0-a0dd-ca1c9e2b2879","flow":"xtls-rprx-vision","packet_encoding":"xudp",
        "tls":{"enabled":true,"server_name":"cdn.example.org","utls":{"enabled":true,"fingerprint":"chrome"},
        "reality":{"enabled":true,"public_key":"pbk","short_id":"0123abcd"}},
        "transport":{"type":"ws","path":"/ws","headers":{"Host":"cdn.example.org"}}}"""

    @Test fun planKeepsServerPortAndGroupsKnownFields() {
        val fields = EditingProtocols.plan("vless", JSONObject(vless))
        val keys = fields.map { it.key }
        // Always-editable server leaves come first, before per-type fields.
        assertEquals("server", keys.first())
        assertEquals("server_port", keys[1])
        assertTrue(keys.containsAll(listOf("uuid", "flow", "tls.server_name", "tls.reality.public_key", "transport.path", "transport.headers.Host")))
        assertEquals(Section.AUTH, fields.first { it.key == "uuid" }.section)
        assertEquals(Section.SECURITY, fields.first { it.key == "tls.server_name" }.section)
        assertEquals(Section.TRANSPORT, fields.first { it.key == "transport.path" }.section)
        assertEquals(Kind.SECRET, fields.first { it.key == "uuid" }.kind)
        assertTrue(fields.first { it.key == "server" }.optional.not())
        // Unknown scalar the config actually carries stays editable; structure keys do not.
        assertEquals("packet_encoding", keys.first { it !in listOf("server", "server_port", "uuid", "flow") && it.startsWith("packet") })
        assertFalse(keys.contains("tls"))
        assertFalse(keys.contains("transport"))
    }

    @Test fun nestedFieldsOnlyAppearWhenParentsExist() {
        val bare = """{"tag":"n","type":"trojan","server":"s","server_port":443,"password":"p"}"""
        val keys = EditingProtocols.plan("trojan", JSONObject(bare)).map { it.key }
        assertFalse(keys.any { it.startsWith("tls.") || it.startsWith("transport.") })
    }

    @Test fun readWriteRoundTripOnlyTouchesOwnedLeaves() {
        val outbound = JSONObject(vless)
        val plan = EditingProtocols.plan("vless", outbound)
        EditingProtocols.writeField(outbound, plan.first { it.key == "server" }, " new.example.org ")
        EditingProtocols.writeField(outbound, plan.first { it.key == "server_port" }, "8443")
        EditingProtocols.writeField(outbound, plan.first { it.key == "tls.server_name" }, "edge.example.org")
        EditingProtocols.writeField(outbound, plan.first { it.key == "transport.headers.Host" }, "host.example.org")
        EditingProtocols.writeField(outbound, plan.first { it.key == "uuid" }, "")
        assertEquals("new.example.org", outbound.getString("server"))
        assertEquals(8443, outbound.getInt("server_port"))
        assertEquals("edge.example.org", outbound.getJSONObject("tls").getString("server_name"))
        assertEquals("host.example.org", outbound.getJSONObject("transport").getJSONObject("headers").getString("Host"))
        // Untouched structure survives: tls.enabled, utls, reality and the tag.
        assertTrue(outbound.getJSONObject("tls").getBoolean("enabled"))
        assertEquals("chrome", outbound.getJSONObject("tls").getJSONObject("utls").getString("fingerprint"))
        assertEquals("pbk", outbound.getJSONObject("tls").getJSONObject("reality").getString("public_key"))
        assertEquals("node", outbound.getString("tag"))
        // Blank secret on an optional field removes the key instead of writing "".
        assertFalse(outbound.has("uuid"))
        assertEquals("xtls-rprx-vision", outbound.getString("flow"))
    }

    @Test fun listAndBooleanLeaves() {
        val outbound = JSONObject("""{"tag":"n","type":"vless","tls":{"alpn":["h2","http/1.1"],"insecure":false}}""")
        val plan = EditingProtocols.plan("vless", outbound)
        assertEquals("h2, http/1.1", EditingProtocols.readField(outbound, plan.first { it.key == "tls.alpn" }))
        EditingProtocols.writeField(outbound, plan.first { it.key == "tls.alpn" }, "h3, h2")
        assertEquals("h3", outbound.getJSONObject("tls").getJSONArray("alpn").getString(0))
        assertEquals("h2", outbound.getJSONObject("tls").getJSONArray("alpn").getString(1))
        assertEquals(Kind.BOOLEAN, plan.first { it.key == "tls.insecure" }.kind)
        EditingProtocols.writeField(outbound, plan.first { it.key == "tls.insecure" }, "true")
        assertTrue(outbound.getJSONObject("tls").getBoolean("insecure"))
        assertThrows(IllegalStateException::class.java) { EditingProtocols.writeField(outbound, plan.first { it.key == "tls.insecure" }, "maybe") }
    }

    @Test fun numberAndMissingParentAreRejected() {
        val outbound = JSONObject(vless)
        val plan = EditingProtocols.plan("vless", outbound)
        assertThrows(IllegalStateException::class.java) { EditingProtocols.writeField(outbound, plan.first { it.key == "server_port" }, "abc") }
        // Removing a parent that disappeared between render and save fails loudly.
        outbound.remove("tls")
        assertThrows(IllegalStateException::class.java) {
            EditingProtocols.writeField(outbound, FieldSpec("tls.server_name", 0, Section.SECURITY), "x")
        }
    }

    @Test fun unchangedSavePreservesUnknownChoicesEmptyLeavesAndLongNumbers() {
        val outbound = JSONObject("""{"tag":"n","type":"shadowsocks","server":"s","server_port":443,
            "method":"2022-blake3-aes-256-gcm","password":"  secret  ","counter":4294967296,
            "note":"","tls":{"server_name":"","enabled":true}}""")
        val before = outbound.toString()
        val plan = EditingProtocols.plan("shadowsocks", outbound)
        val originals = plan.associate { it.key to EditingProtocols.readField(outbound, it) }
        EditingProtocols.applyChanges(outbound, plan, originals, originals)
        assertEquals(before, outbound.toString())
        EditingProtocols.applyChanges(outbound, plan, originals, originals + ("server" to "new.example.org"))
        assertEquals("new.example.org", outbound.getString("server"))
        assertEquals("2022-blake3-aes-256-gcm", outbound.getString("method"))
        assertEquals("  secret  ", outbound.getString("password"))
        assertEquals(4294967296L, outbound.getLong("counter"))
        assertTrue(outbound.has("note"))
        assertTrue(outbound.getJSONObject("tls").has("server_name"))
    }

    @Test fun changedSecretsKeepWhitespaceAndNumbersKeepLongRange() {
        val outbound = JSONObject("""{"password":"old","counter":1}""")
        EditingProtocols.writeField(outbound, FieldSpec("password", 0, Section.AUTH, Kind.SECRET), " new secret ")
        EditingProtocols.writeField(outbound, FieldSpec("counter", 0, Section.SERVER, Kind.NUMBER), "4294967296")
        assertEquals(" new secret ", outbound.getString("password"))
        assertEquals(4294967296L, outbound.getLong("counter"))
    }

    @Test fun unknownProtocolStaysEditableThroughPassthrough() {
        val outbound = JSONObject("""{"tag":"n","type":"wireguard","secret_key":"abc","mtu":1408,"reserved":true}""")
        val plan = EditingProtocols.plan("wireguard", outbound)
        val secret = plan.first { it.key == "secret_key" }
        assertEquals(Kind.SECRET, secret.kind)
        assertEquals(Kind.NUMBER, plan.first { it.key == "mtu" }.kind)
        assertEquals(Kind.BOOLEAN, plan.first { it.key == "reserved" }.kind)
    }
}
