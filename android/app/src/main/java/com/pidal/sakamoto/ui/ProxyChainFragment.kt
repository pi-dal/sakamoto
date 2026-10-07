package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.ConfigRepository
import org.json.JSONObject

/** Exit rows open one coherent server form, preserving host fallback as read-only. */
class ProxyChainFragment : androidx.fragment.app.Fragment() {
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View = android.widget.FrameLayout(requireContext()).apply { addView(build().root) }
    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        parentFragmentManager.setFragmentResultListener("config-updated", viewLifecycleOwner) { _, _ ->
            (this.view as? android.widget.FrameLayout)?.let { it.removeAllViews(); it.addView(build().root) }
        }
    }
    private fun build(): GroupedPage {
        val page = GroupedPage(requireContext())
        val saved = ConfigRepository.load(requireContext())
        val root = runCatching { JSONObject(saved.generatedContent) }.getOrNull() ?: return page
        val outbounds = root.getJSONArray("outbounds")
        val exits = (0 until outbounds.length()).map { outbounds.getJSONObject(it) }.filter { it.optString("type") in setOf("socks", "http") }
        val group = page.section(getString(R.string.proxy_exit_effective))
        for (exit in exits) {
            val tag = exit.getString("tag")
            val credentials = if (exit.optString("username").isNotEmpty() || exit.optString("password").isNotEmpty())
                getString(R.string.ux_editing_exit_auth) else getString(R.string.ux_editing_exit_plain)
            val detail = listOf(
                exit.optString("type"),
                exit.optString("server") + ":" + exit.optInt("server_port"),
                credentials,
                exit.optString("detour").ifEmpty { getString(R.string.proxy_direct_dial) },
            ).joinToString(" · ")
            page.row(group, tag, detail, R.drawable.ic_node) {
                (requireActivity() as MainActivity).openChild(EditorFragment().apply {
                    arguments = Bundle().apply { putString("kind", "outbound"); putString("tag", tag) }
                }, getString(R.string.profile_edit_title))
            }
        }
        if (exits.isEmpty()) page.row(group, getString(R.string.proxy_exit_none))
        page.note(getString(R.string.device_edits_note))
        val metadata = runCatching { JSONObject(saved.hostMetadata) }.getOrDefault(JSONObject())
        val priorities = metadata.optJSONObject("fallbacks") ?: JSONObject()
        val host = page.section(getString(R.string.proxy_fallback_title))
        priorities.keys().forEach { tag ->
            val members = priorities.optJSONArray(tag)
            page.row(host, tag, if (members == null) "—" else (0 until members.length()).joinToString(" → ") { members.getString(it) })
        }
        page.note(getString(R.string.proxy_fallback_note))
        return page
    }
}
