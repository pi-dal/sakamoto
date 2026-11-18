package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import com.pidal.sakamoto.runtime.ConfigRepository

class RegionalDNSFragment : androidx.fragment.app.Fragment() {
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext())
        var original = ConfigRepository.load(requireContext())
        val values = original.regionalDNS.toMutableMap()
        val group = page.section("Encrypted DNS path")
        val scope = com.pidal.sakamoto.runtime.AutomaticConnectionSettings.load(requireContext())
        if (scope.onlySelectedApps) {
            page.note("VPN access is limited to ${scope.packages.size} selected apps. Include your input method in Automatic connection for its voice lookups to use this DNS path.")
        } else {
            page.note("Regional DNS applies to apps included in this VPN. Check the profile’s app exclusions when using an input method.")
        }
        val fields = mapOf("domains" to "Exact domains, separated by commas", "resolver_ip" to "Resolver IP", "tls_name" to "Resolver TLS name", "relay" to "Approved relay node")
        for ((key, label) in fields) {
            values.putIfAbsent(key, "")
            lateinit var row: GroupedPage.Row
            row = page.row(group, label, values.getValue(key)) {
                EditDialogs.text(requireContext(), label, values.getValue(key)) { value ->
                    values[key] = value.trim()
                    row.detail(values.getValue(key))
                }
            }
        }
        val notice = page.note("Clear the domains to use your usual DNS policy. If the relay is unavailable, matching lookups fail.")
        page.button(group, "Approve relay and save", primary = true) {
            try {
                ConfigRepository.saveRegionalDNS(requireContext(), original, values)
                original = ConfigRepository.load(requireContext())
                notice.text = "Saved. Apply changes to use this DNS path."
            } catch (error: Exception) { notice.text = "Check the exact domains, resolver and approved relay. Reopen if configuration changed." }
        }
        return page.root
    }
}
