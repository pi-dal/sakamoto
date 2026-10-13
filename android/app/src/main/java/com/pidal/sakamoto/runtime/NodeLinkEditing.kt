package com.pidal.sakamoto.runtime

import org.json.JSONObject
import java.net.URI
import okio.ByteString.Companion.decodeBase64
import okio.ByteString.Companion.toByteString

object NodeLinkEditing {
    data class Endpoint(val remark: String, val server: String, val port: Int?)
    private fun decode(text: String): ByteArray? = text.decodeBase64()?.toByteArray()
    private fun vmess(raw: String): JSONObject? {
        if (!raw.startsWith("vmess://", true)) return null
        return decode(raw.substring(8))?.let { runCatching { JSONObject(String(it, Charsets.UTF_8)) }.getOrNull() }?.takeIf { it.has("add") }
    }
    private fun uri(raw: String): URI {
        var value = URI(raw)
        if (value.scheme in setOf("vmess", "vless", "socks", "socks5") && value.rawUserInfo == null) {
            val authority = raw.substringAfter("://").substringBefore('?').substringBefore('#')
            decode(authority)?.let { bytes ->
                val decoded = String(bytes, Charsets.UTF_8)
                if (decoded.contains('@')) value = URI(value.scheme + "://" + decoded + (value.rawQuery?.let { "?$it" } ?: "") + (value.rawFragment?.let { "#$it" } ?: ""))
            }
        }
        require(!value.host.isNullOrEmpty()) { "This node is missing its server" }
        return value
    }
    fun endpoint(raw: String): Endpoint {
        vmess(raw)?.let { return Endpoint(it.optString("ps", it.getString("add")), it.getString("add"), it.optString("port", "443").toIntOrNull() ?: 443) }
        val value = uri(raw)
        val items = value.rawQuery.orEmpty().split('&')
        val remark = value.fragment?.takeIf { it.isNotEmpty() } ?: items.firstOrNull { it.startsWith("remarks=") }?.substringAfter('=')?.let { java.net.URLDecoder.decode(it, "UTF-8") } ?: value.host
        return Endpoint(remark, value.host.removePrefix("[").removeSuffix("]"), if (items.any { it.startsWith("mport=") && it.substringAfter('=').isNotEmpty() }) null else value.port.takeIf { it > 0 } ?: if (value.scheme in setOf("socks", "socks5")) 1080 else 443)
    }
    fun update(raw: String, remark: String, server: String, port: Int?): String {
        val name = remark.trim(); val host = server.trim()
        require(name.isNotEmpty() && name.none { it == '\n' || it == '\r' || it == '\u0000' } && host.isNotEmpty() && host.none { it.isWhitespace() } && !host.contains("://") && !host.contains('/')) { "Enter a remark and an IP address or hostname" }
        require(port == null || port in 1..65535) { "Port must be 1–65535" }
        vmess(raw)?.let { json ->
            json.put("ps", name).put("add", host); if (port != null) json.put("port", port.toString())
            return "vmess://" + json.toString().toByteArray(Charsets.UTF_8).toByteString().base64()
        }
        val value = uri(raw)
        val authority = URI(null, null, host, port ?: value.port, null, null, null).rawAuthority
        val fragment = URI(null, null, null, name).rawFragment
        return value.scheme + "://" + (value.rawUserInfo?.let { "$it@" } ?: "") + authority + value.rawPath.orEmpty() + (value.rawQuery?.let { "?$it" } ?: "") + "#$fragment"
    }
}
