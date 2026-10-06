package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import com.google.android.material.snackbar.Snackbar
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import io.nekohasekai.mobilecore.Mobilecore

/** Sources have visible rows and edit buttons, not a nested list of alert menus. */
class SourcesManagerFragment : androidx.fragment.app.Fragment() {
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View = android.widget.FrameLayout(requireContext()).apply { addView(build().root) }
    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        parentFragmentManager.setFragmentResultListener("config-updated", viewLifecycleOwner) { _, _ -> refresh() }
    }
    private fun refresh() { (view as? android.widget.FrameLayout)?.let { it.removeAllViews(); it.addView(build().root) } }
    private fun build(): GroupedPage {
        val page = GroupedPage(requireContext())
        val saved = ConfigRepository.load(requireContext())
        val nodes = page.section(getString(R.string.profile_source_nodes))
        page.button(nodes, getString(R.string.add_node_action)) { AddNodeSheet().show(parentFragmentManager, "add-node") }
        for (raw in saved.nodes) {
            val info = runCatching { Mobilecore.parseShareLink(raw) }.getOrNull()
            fun editNode() { AddNodeSheet().apply { arguments = Bundle().apply { putString("original", raw) } }.show(parentFragmentManager, "edit-node") }
            val row = page.row(nodes, info?.tag ?: getString(R.string.profile_invalid_source), info?.type.orEmpty(), R.drawable.ic_node, navigates = false) { editNode() }
            row.actionIcon(R.drawable.ic_edit, getString(R.string.edit)) { editNode() }
            fun removeMenu() {
                val menu = androidx.appcompat.widget.PopupMenu(requireContext(), row)
                menu.menu.add(R.string.remove)
                menu.setOnMenuItemClickListener {
                    removeWithUndo(saved) { current -> current.copy(nodes = current.nodes.filterNot { it == raw }, sourceNeedsGenerate = true) }; true
                }
                menu.show()
            }
            row.actionIcon(R.drawable.ic_more, getString(R.string.row_actions)) { removeMenu() }
            row.setOnLongClickListener { removeMenu(); true }
        }
        val subscriptions = page.section(getString(R.string.profile_subscriptions))
        page.button(subscriptions, getString(R.string.add_subscription_action)) { AddSubscriptionSheet().show(parentFragmentManager, "add-subscription") }
        saved.subscriptions.forEach { source ->
            fun editSource() { AddSubscriptionSheet().apply { arguments = Bundle().apply { putString("name", source.first); putString("url", source.second); putString("format", source.third) } }.show(parentFragmentManager, "edit-subscription") }
            val row = page.row(subscriptions, source.first, source.third, R.drawable.ic_sync, navigates = false) { editSource() }
            row.actionIcon(R.drawable.ic_edit, getString(R.string.edit)) { editSource() }
            fun removeMenu() {
                val menu = androidx.appcompat.widget.PopupMenu(requireContext(), row)
                menu.menu.add(R.string.remove)
                menu.setOnMenuItemClickListener {
                    removeWithUndo(saved) { current -> current.copy(subscriptions = current.subscriptions.filterNot { it == source }, sourceNeedsGenerate = true) }; true
                }
                menu.show()
            }
            row.actionIcon(R.drawable.ic_more, getString(R.string.row_actions)) { removeMenu() }
            row.setOnLongClickListener { removeMenu(); true }
        }
        page.note(getString(R.string.profile_sources_note))
        return page
    }
    private fun removeWithUndo(before: ConfigRepository.StagedConfig, edit: (ConfigRepository.StagedConfig) -> ConfigRepository.StagedConfig) {
        val current = ConfigRepository.load(requireContext())
        if (current != before) { refresh(); return }
        val after = edit(current)
        ConfigRepository.save(requireContext(), after); MobilecoreRuntime.configEvent("modified"); refresh()
        view?.let { anchor -> Snackbar.make(anchor, R.string.source_removed, Snackbar.LENGTH_LONG).setAction(R.string.undo) {
            if (ConfigRepository.load(requireContext()) == after) { ConfigRepository.save(requireContext(), before); refresh() }
            else Snackbar.make(anchor, R.string.editor_conflict, Snackbar.LENGTH_LONG).show()
        }.show() }
    }
}
