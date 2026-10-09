package com.pidal.sakamoto

import com.pidal.sakamoto.runtime.AutomaticConnectionSettings
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class AutomaticConnectionSettingsTest {
    private val config = """{"inbounds":[{"type":"tun","include_package":["existing.app"]}],"outbounds":[{"type":"selector","tag":"MainProxy","outbounds":["node"]},{"type":"trojan","tag":"node"},{"type":"direct","tag":"direct"}],"route":{"final":"direct","rules":[{"action":"sniff"},{"action":"hijack-dns","protocol":"dns"},{"action":"reject","domain":["blocked.example"]}]}}"""

    @Test fun domainURLsNormalizeAndRejectCredentials() {
        assertEquals(listOf("example.com", "api.example.org"), AutomaticConnectionSettings.normalizeDomains("https://Example.com/path example.com,api.example.org"))
        listOf("https://user:{{PASSWORD_dzhli4me}}@example.com", "ftp://example.com", "bad_host", "-bad.example").forEach {
            assertThrows(IllegalArgumentException::class.java) { AutomaticConnectionSettings.normalizeDomains(it) }
        }
    }

    @Test fun conditionsKeepDNSAndSniffAheadOfProxyRulesAndPreserveBase() {
        val settings = AutomaticConnectionSettings(domains = listOf("example.com"), packages = listOf("selected.app"), proxyOutbound = "MainProxy")
        val next = JSONObject(settings.applying(config))
        val rules = next.getJSONObject("route").getJSONArray("rules")
        assertEquals("sniff", rules.getJSONObject(0).getString("action"))
        assertEquals("hijack-dns", rules.getJSONObject(1).getString("action"))
        assertEquals("example.com", rules.getJSONObject(2).getJSONArray("domain_suffix").getString(0))
        assertEquals("selected.app", rules.getJSONObject(3).getJSONArray("package_name").getString(0))
        assertEquals("reject", rules.getJSONObject(4).getString("action"))
        assertEquals("direct", next.getJSONObject("route").getString("final"))
        assertEquals("existing.app", next.getJSONArray("inbounds").getJSONObject(0).getJSONArray("include_package").getString(0))
        assertEquals(3, JSONObject(config).getJSONObject("route").getJSONArray("rules").length())
    }

    @Test fun missingOrDirectOutboundCannotBeUsedAsProxy() {
        listOf("", "missing", "direct").forEach { outbound ->
            assertThrows(IllegalArgumentException::class.java) { AutomaticConnectionSettings(domains = listOf("example.com"), proxyOutbound = outbound).applying(config) }
        }
        assertEquals(config, AutomaticConnectionSettings().applying(config))
    }
}
