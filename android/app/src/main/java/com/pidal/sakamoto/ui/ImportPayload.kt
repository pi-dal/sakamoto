package com.pidal.sakamoto.ui

/** Classifies user-provided text without performing IO or saving anything. */
object ImportPayload {
    enum class Kind { NODE, URL, CONFIG_JSON, CONF }
    data class Candidate(val kind: Kind, val text: String)
    private val nodeSchemes = setOf("ss", "ssr", "vmess", "vless", "trojan", "hysteria", "hysteria2", "hy2", "tuic", "socks", "socks5", "http", "https", "anytls", "naive", "ssh")
    fun detect(raw: String): Candidate {
        require(raw.toByteArray(Charsets.UTF_8).size <= 1_048_576) { "Import is too large" }
        val text = raw.trim().removePrefix("\uFEFF").trim()
        require(text.isNotEmpty()) { "No importable text" }
        val kind = when {
            text.startsWith("{") -> Kind.CONFIG_JSON
            text.startsWith("[") -> Kind.CONF
            '\n' in text || '\r' in text -> error("Import one link at a time")
            else -> {
                val scheme = text.substringBefore("://", "").lowercase()
                if (scheme == "http" || scheme == "https") {
                    val uri = java.net.URI(text)
                    require(!uri.host.isNullOrEmpty()) { "Invalid URL" }
                    Kind.URL
                } else {
                    require(scheme in nodeSchemes) { "Unsupported import" }
                    Kind.NODE
                }
            }
        }
        return Candidate(kind, text)
    }
}
