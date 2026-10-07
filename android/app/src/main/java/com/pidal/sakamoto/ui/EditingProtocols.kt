package com.pidal.sakamoto.ui

import com.pidal.sakamoto.R
import org.json.JSONArray
import org.json.JSONObject

/**
 * Pure protocol-aware plan for the node editor: which fields a sing-box
 * outbound of a given type shows and how each value reads from and writes to
 * the saved JSON.
 *
 * No protocol conversion or structure generation happens here. Nested
 * security/transport fields surface only when their parent objects already
 * exist in the outbound, every write touches exactly one leaf key, and all
 * other keys survive the edit verbatim (ConfigEdits stays untouched).
 */
object EditingProtocols {

    enum class Kind { TEXT, SECRET, NUMBER, LIST, BOOLEAN, CHOICE }

    enum class Section { SERVER, AUTH, SECURITY, TRANSPORT }

    data class FieldSpec(
        /** Leaf path from the outbound object, e.g. `tls.server_name`. */
        val key: String,
        /** Resource label; 0 falls back to a prettified key name. */
        val labelRes: Int,
        val section: Section,
        val kind: Kind = Kind.TEXT,
        val options: List<String> = emptyList(),
        /** Blank input removes the key instead of failing. */
        val optional: Boolean = true,
    )

    /** Structural or separately-handled keys never become generic fields. */
    private val structural = setOf(
        "tag", "type", "server", "server_port", "detour",
        "tls", "transport", "multiplex", "outbounds", "rules",
    )

    private val secretNames = setOf("password", "uuid", "private_key", "auth_str", "token", "secret", "psk")

    private fun looksSecret(key: String): Boolean = key.lowercase() in secretNames ||
        key.lowercase().let { it.contains("password") || it.contains("secret") || it.contains("token") }

    private val methodOptions = listOf(
        "auto", "none", "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "chacha20-poly1305",
    )

    private data class Known(
        val key: String,
        val labelRes: Int,
        val section: Section,
        val kind: Kind = Kind.TEXT,
        val options: List<String>? = null,
        val optional: Boolean = true,
    )

    private val plans: Map<String, List<Known>> = mapOf(
        "socks" to listOf(
            Known("username", R.string.ux_editing_field_username, Section.AUTH),
            Known("password", R.string.ux_editing_field_password, Section.AUTH, Kind.SECRET),
        ),
        "http" to listOf(
            Known("username", R.string.ux_editing_field_username, Section.AUTH),
            Known("password", R.string.ux_editing_field_password, Section.AUTH, Kind.SECRET),
        ),
        "vmess" to listOf(
            Known("uuid", R.string.ux_editing_field_uuid, Section.AUTH, Kind.SECRET),
            Known("alter_id", R.string.ux_editing_field_alter_id, Section.SERVER, Kind.NUMBER),
            Known("security", R.string.ux_editing_field_encryption, Section.SERVER, Kind.CHOICE,
                listOf("auto", "none", "aes-128-gcm", "chacha20-poly1305", "zero")),
        ),
        "vless" to listOf(
            Known("uuid", R.string.ux_editing_field_uuid, Section.AUTH, Kind.SECRET),
            Known("flow", R.string.ux_editing_field_flow, Section.SERVER, Kind.CHOICE,
                listOf("", "xtls-rprx-vision")),
        ),
        "trojan" to listOf(
            Known("password", R.string.ux_editing_field_password, Section.AUTH, Kind.SECRET),
        ),
        "shadowsocks" to listOf(
            Known("method", R.string.ux_editing_field_method, Section.SERVER, Kind.CHOICE, methodOptions, optional = false),
            Known("password", R.string.ux_editing_field_password, Section.AUTH, Kind.SECRET),
        ),
        "hysteria" to listOf(
            Known("auth_str", R.string.ux_editing_field_auth_str, Section.AUTH, Kind.SECRET),
            Known("up_mbps", R.string.ux_editing_field_up_mbps, Section.SERVER, Kind.NUMBER),
            Known("down_mbps", R.string.ux_editing_field_down_mbps, Section.SERVER, Kind.NUMBER),
        ),
        "hysteria2" to listOf(
            Known("password", R.string.ux_editing_field_password, Section.AUTH, Kind.SECRET),
            Known("up_mbps", R.string.ux_editing_field_up_mbps, Section.SERVER, Kind.NUMBER),
            Known("down_mbps", R.string.ux_editing_field_down_mbps, Section.SERVER, Kind.NUMBER),
        ),
        "tuic" to listOf(
            Known("uuid", R.string.ux_editing_field_uuid, Section.AUTH, Kind.SECRET),
            Known("password", R.string.ux_editing_field_password, Section.AUTH, Kind.SECRET),
            Known("congestion_control", R.string.ux_editing_field_congestion, Section.SERVER, Kind.CHOICE,
                listOf("cubic", "new_reno", "bbr")),
        ),
        "ssh" to listOf(
            Known("user", R.string.ux_editing_field_username, Section.AUTH),
            Known("password", R.string.ux_editing_field_password, Section.AUTH, Kind.SECRET),
            Known("private_key", R.string.ux_editing_field_private_key, Section.AUTH, Kind.SECRET),
        ),
        "anytls" to listOf(
            Known("password", R.string.ux_editing_field_password, Section.AUTH, Kind.SECRET),
        ),
        "naive" to listOf(
            Known("user", R.string.ux_editing_field_username, Section.AUTH),
            Known("password", R.string.ux_editing_field_password, Section.AUTH, Kind.SECRET),
        ),
    )

    /**
     * Ordered editable fields for one outbound. Known per-type fields come
     * first, then any other scalar the saved outbound actually carries
     * (unknown protocols stay editable without inventing structure), then
     * existing security/transport leaves.
     */
    fun plan(type: String, outbound: JSONObject): List<FieldSpec> {
        val known = plans[type].orEmpty()
        val fields = mutableListOf(
            FieldSpec("server", R.string.proxy_server, Section.SERVER, optional = false),
            FieldSpec("server_port", R.string.proxy_port, Section.SERVER, Kind.NUMBER, optional = false),
        )
        for (entry in known) {
            fields += FieldSpec(entry.key, entry.labelRes, entry.section, entry.kind, entry.options.orEmpty(), entry.optional)
        }
        for (key in outbound.keys()) {
            if (key in structural || known.any { it.key == key }) continue
            when (val value = outbound.get(key)) {
                is String -> fields += if (looksSecret(key)) {
                    FieldSpec(key, 0, Section.SERVER, Kind.SECRET)
                } else {
                    FieldSpec(key, 0, Section.SERVER)
                }
                is Int, is Long -> fields += FieldSpec(key, 0, Section.SERVER, Kind.NUMBER)
                is Boolean -> fields += FieldSpec(key, 0, Section.SERVER, Kind.BOOLEAN, listOf("true", "false"))
                else -> {} // objects/arrays keep their saved value untouched
            }
        }
        val tls = outbound.optJSONObject("tls")
        if (tls != null) {
            fields += FieldSpec("tls.server_name", R.string.node_tls_name, Section.SECURITY)
            if (tls.has("alpn")) fields += FieldSpec("tls.alpn", R.string.ux_editing_field_alpn, Section.SECURITY, Kind.LIST)
            if (tls.has("insecure")) fields += FieldSpec("tls.insecure", R.string.ux_editing_field_insecure, Section.SECURITY, Kind.BOOLEAN, listOf("true", "false"))
            if (tls.optJSONObject("utls") != null) {
                fields += FieldSpec("tls.utls.fingerprint", R.string.ux_editing_field_fingerprint, Section.SECURITY)
            }
            if (tls.optJSONObject("reality") != null) {
                fields += FieldSpec("tls.reality.public_key", R.string.ux_editing_field_reality_key, Section.SECURITY)
                fields += FieldSpec("tls.reality.short_id", R.string.ux_editing_field_reality_id, Section.SECURITY)
            }
        }
        val transport = outbound.optJSONObject("transport")
        if (transport != null) {
            when (transport.optString("type")) {
                "ws", "httpupgrade" -> {
                    fields += FieldSpec("transport.path", R.string.ux_editing_field_path, Section.TRANSPORT)
                    if (transport.optJSONObject("headers")?.has("Host") == true) {
                        fields += FieldSpec("transport.headers.Host", R.string.ux_editing_field_host, Section.TRANSPORT)
                    }
                }
                "http" -> {
                    fields += FieldSpec("transport.path", R.string.ux_editing_field_path, Section.TRANSPORT)
                    if (transport.has("host")) fields += FieldSpec("transport.host", R.string.ux_editing_field_host, Section.TRANSPORT, Kind.LIST)
                }
                "grpc" -> fields += FieldSpec("transport.service_name", R.string.ux_editing_field_service_name, Section.TRANSPORT)
            }
        }
        return fields
    }

    /** Pretty label for keys without a resource (shows the real config key). */
    fun fallbackLabel(key: String): String = key.substringAfterLast('.').replace('_', ' ').replaceFirstChar { it.uppercase() }

    /** Reads one leaf into editor text; arrays join with ", ". */
    fun readField(outbound: JSONObject, spec: FieldSpec): String {
        val path = spec.key.split('.')
        var current: Any = outbound
        for (part in path.dropLast(1)) {
            current = (current as? JSONObject)?.optJSONObject(part) ?: return ""
        }
        val value = (current as? JSONObject)?.opt(path.last()) ?: return ""
        return if (spec.kind == Kind.LIST && value is JSONArray) {
            (0 until value.length()).joinToString(", ") { value.get(it).toString() }
        } else value.toString()
    }

    /** A form save must not normalize untouched values, including unknown choices,
     * absent optional fields, empty strings, secrets, or 64-bit numbers. */
    fun applyChanges(outbound: JSONObject, specs: List<FieldSpec>, originals: Map<String, String>, values: Map<String, String>) {
        for (spec in specs.distinctBy { it.key }) {
            val raw = values[spec.key] ?: continue
            if (raw != originals[spec.key]) writeField(outbound, spec, raw)
        }
    }

    /** Writes one leaf; blank input on an optional field removes the key. */
    fun writeField(outbound: JSONObject, spec: FieldSpec, raw: String) {
        val path = spec.key.split('.')
        val parent = path.dropLast(1).fold(outbound) { current, part ->
            current.optJSONObject(part) ?: error("Section \"$part\" is missing in this outbound")
        }
        val key = path.last()
        // Whitespace is significant in credentials; never silently trim a secret.
        val value = if (spec.kind == Kind.SECRET) raw else raw.trim()
        if (value.isEmpty()) {
            check(spec.optional) { "This field is required" }
            parent.remove(key)
            return
        }
        when (spec.kind) {
            Kind.NUMBER -> parent.put(key, value.toLongOrNull() ?: error("Enter a whole number"))
            Kind.LIST -> parent.put(key, JSONArray(value.split(',').map { it.trim() }.filter { it.isNotEmpty() }))
            Kind.BOOLEAN -> {
                check(value in setOf("true", "false")) { "Use on or off" }
                parent.put(key, value.toBoolean())
            }
            Kind.CHOICE -> {
                check(spec.options.contains(value)) { "Choose one of the listed options" }
                parent.put(key, value)
            }
            else -> parent.put(key, value)
        }
    }
}
