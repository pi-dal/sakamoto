package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.ConfigEdits
import com.pidal.sakamoto.runtime.ConfigRepository
import org.json.JSONObject

class TunnelSettingsFragment : androidx.fragment.app.Fragment() {
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View = android.widget.FrameLayout(requireContext()).apply { addView(build().root) }
    private fun save(content: String) {
        ConfigRepository.saveGeneratedEdit(requireContext(), content)
        parentFragmentManager.setFragmentResult("config-updated", Bundle())
        (view as? android.widget.FrameLayout)?.let { it.removeAllViews(); it.addView(build().root) }
    }
    private fun build(): GroupedPage {
        val page = GroupedPage(requireContext())
        val config = ConfigRepository.load(requireContext()).generatedContent
        val root = runCatching { JSONObject(config) }.getOrNull() ?: return page
        val group = page.section(getString(R.string.tunnel_settings))
        val level = root.optJSONObject("log")?.optString("level", "info") ?: "info"
        page.row(group, getString(R.string.log_level), level, R.drawable.ic_info) {
            EditDialogs.choice(requireContext(), getString(R.string.log_level), listOf("trace", "debug", "info", "warn", "error", "fatal", "panic"), level) {
                save(ConfigEdits.logLevel(ConfigRepository.load(requireContext()).generatedContent, it))
            }
        }
        val inbounds = root.optJSONArray("inbounds")
        val tun = inbounds?.let { array -> (0 until array.length()).map { array.getJSONObject(it) }.firstOrNull { it.optString("type") == "tun" } }
        if (tun != null) {
            page.row(group, getString(R.string.tun_stack), tun.optString("stack", "mixed"), R.drawable.ic_route) {
                EditDialogs.choice(requireContext(), getString(R.string.tun_stack), listOf("system", "gvisor", "mixed"), tun.optString("stack", "mixed")) {
                    save(ConfigEdits.tunSetting(ConfigRepository.load(requireContext()).generatedContent, "stack", it))
                }
            }
            page.row(group, getString(R.string.strict_routing), getString(if (tun.optBoolean("strict_route")) R.string.setting_on else R.string.setting_off), R.drawable.ic_route) {
                EditDialogs.choice(requireContext(), getString(R.string.strict_routing), listOf(getString(R.string.setting_off), getString(R.string.setting_on)), getString(if (tun.optBoolean("strict_route")) R.string.setting_on else R.string.setting_off)) {
                    save(ConfigEdits.tunSetting(ConfigRepository.load(requireContext()).generatedContent, "strict_route", it == getString(R.string.setting_on)))
                }
            }
        }
        page.note(getString(R.string.device_edits_note))
        return page
    }
}
