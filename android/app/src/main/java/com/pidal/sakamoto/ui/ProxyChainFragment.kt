package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.ConfigEdits
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.ProxyChainSettings
import org.json.JSONObject

/** Saves chain intent without modifying the running tunnel or protocol credentials. */
class ProxyChainFragment : androidx.fragment.app.Fragment() {
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View = android.widget.FrameLayout(requireContext()).apply { addView(build().root) }
    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        parentFragmentManager.setFragmentResultListener("config-updated", viewLifecycleOwner) { _, _ -> refresh() }
    }
    private fun refresh() {
        (view as? android.widget.FrameLayout)?.let { it.removeAllViews(); it.addView(build().root) }
    }
    private fun build(): GroupedPage {
        val page = GroupedPage(requireContext())
        val saved = ConfigRepository.load(requireContext())
        val root = runCatching { JSONObject(saved.generatedContent) }.getOrNull() ?: return page
        val outbounds = root.getJSONArray("outbounds")
        val rows = (0 until outbounds.length()).map { outbounds.getJSONObject(it) }
        var settings = saved.proxyChain ?: ProxyChainSettings.imported(saved.generatedContent) ?: ProxyChainSettings()
        val edit = page.section(getString(R.string.proxy_chain_title))
        val enabled = page.toggle(edit, getString(R.string.proxy_chain_use), settings.enabled)
        enabled.setOnCheckedChangeListener { _, checked -> settings = settings.copy(enabled = checked) }
        lateinit var upstreamRow: GroupedPage.Row
        upstreamRow = page.row(edit, getString(R.string.proxy_chain_upstream), settings.upstream) {
            val options = rows.filter { it.optString("tag") != settings.exit && it.optString("type") !in setOf("direct", "block", "dns") }.map { it.getString("tag") }
            EditDialogs.choice(requireContext(), getString(R.string.proxy_chain_upstream), options, settings.upstream) {
                settings = settings.copy(upstream = it); upstreamRow.detail(it)
            }
        }
        lateinit var exitRow: GroupedPage.Row
        exitRow = page.row(edit, getString(R.string.proxy_exit_requested), settings.exit.ifEmpty { getString(R.string.profile_not_selected) }) {
            EditDialogs.choice(requireContext(), getString(R.string.proxy_exit_requested), ConfigEdits.chainableOutboundTags(saved.generatedContent), settings.exit) {
                settings = settings.copy(exit = it); exitRow.detail(it)
            }
        }
        val notice = page.note(getString(R.string.proxy_chain_save_note))
        page.button(edit, getString(R.string.proxy_chain_save), primary = true) {
            try {
                ConfigRepository.saveProxyChain(requireContext(), settings, saved)
                refresh()
            } catch (error: Exception) { notice.text = error.message ?: getString(R.string.invalid_value) }
        }
        val group = page.section(getString(R.string.proxy_exit_effective))
        for (exit in rows.filter { it.optString("tag") in ConfigEdits.chainableOutboundTags(saved.generatedContent) }) {
            val tag = exit.getString("tag")
            page.row(group, tag, exit.optString("type"), R.drawable.ic_node) {
                (requireActivity() as MainActivity).openChild(EditorFragment().apply {
                    arguments = Bundle().apply { putString("kind", "outbound"); putString("tag", tag) }
                }, getString(R.string.profile_edit_title))
            }
        }
        page.note(getString(R.string.device_edits_note))
        return page
    }
}
