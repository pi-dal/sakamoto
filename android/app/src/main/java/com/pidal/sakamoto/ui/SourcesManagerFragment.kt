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

/**
 * Sources are rows with their actions visible: tapping edits directly and a
 * delete action removes with guarded Undo — no nested popup menus. The
 * generation boundary stays explicit: edits stage locally, the host regenerates.
 */
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
            row.actionIcon(R.drawable.ic_edit, getString(R.string.profile_edit_node, info?.tag ?: getString(R.string.profile_invalid_source))) { editNode() }
            val remove = { removeWithUndo(saved) { current -> current.copy(nodes = current.nodes.filterNot { it == raw }, sourceNeedsGenerate = true) } }
            row.actionIcon(R.drawable.ux_profiles_delete, getString(R.string.remove)) { remove() }.apply {
                // actionIcon is declared View; the button it builds is always an AppCompatImageButton.
                val image = this as androidx.appcompat.widget.AppCompatImageButton
                image.imageTintList = android.content.res.ColorStateList.valueOf(
                    com.google.android.material.color.MaterialColors.getColor(image, com.google.android.material.R.attr.colorError))
            }
            row.setOnLongClickListener { remove(); true }
        }
        val subscriptions = page.section(getString(R.string.profile_subscriptions))
        page.button(subscriptions, getString(R.string.add_subscription_action)) { AddSubscriptionSheet().show(parentFragmentManager, "add-subscription") }
        saved.subscriptions.forEach { source ->
            fun editSource() { AddSubscriptionSheet().apply { arguments = Bundle().apply { putString("name", source.first); putString("url", source.second); putString("format", source.third) } }.show(parentFragmentManager, "edit-subscription") }
            val row = page.row(subscriptions, source.first, source.third, R.drawable.ic_sync, navigates = false) { editSource() }
            row.actionIcon(R.drawable.ic_edit, getString(R.string.profile_edit_node, source.first)) { editSource() }
            val remove = { removeWithUndo(saved) { current -> current.copy(subscriptions = current.subscriptions.filterNot { it == source }, sourceNeedsGenerate = true) } }
            row.actionIcon(R.drawable.ux_profiles_delete, getString(R.string.remove)) { remove() }.apply {
                // actionIcon is declared View; the button it builds is always an AppCompatImageButton.
                val image = this as androidx.appcompat.widget.AppCompatImageButton
                image.imageTintList = android.content.res.ColorStateList.valueOf(
                    com.google.android.material.color.MaterialColors.getColor(image, com.google.android.material.R.attr.colorError))
            }
            row.setOnLongClickListener { remove(); true }
        }
        page.note(getString(R.string.ux_profiles_sources_note))
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
