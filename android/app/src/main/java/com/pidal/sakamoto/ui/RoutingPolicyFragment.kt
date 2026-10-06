package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.appcompat.widget.PopupMenu
import com.google.android.material.snackbar.Snackbar
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.ConfigEdits
import com.pidal.sakamoto.runtime.RoutingSnapshot

/** Rule rows open editors directly; menu handles ordering/deletion with undo. */
class RoutingPolicyFragment : androidx.fragment.app.Fragment() {
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View = android.widget.FrameLayout(requireContext()).apply { addView(build().root) }
    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        parentFragmentManager.setFragmentResultListener("config-updated", viewLifecycleOwner) { _, _ -> refresh() }
    }
    private fun refresh() { (view as? android.widget.FrameLayout)?.let { it.removeAllViews(); it.addView(build().root) } }
    private fun save(content: String) { ConfigRepository.saveGeneratedEdit(requireContext(), content); refresh() }
    private fun editor(index: Int) {
        (requireActivity() as MainActivity).openChild(EditorFragment().apply {
            arguments = Bundle().apply { putString("kind", "rule"); putInt("index", index) }
        }, getString(if (index < 0) R.string.add_policy_action else R.string.edit_rule))
    }
    private fun build(): GroupedPage {
        val page = GroupedPage(requireContext())
        val content = ConfigRepository.load(requireContext()).generatedContent
        val snapshot = runCatching { RoutingSnapshot.parse(content) }.getOrNull()
        val group = page.section(getString(R.string.policy_effective_rules))
        page.button(group, getString(R.string.add_policy_action), primary = true) { editor(-1) }
        if (snapshot == null) { page.row(group, getString(R.string.apply_error_no_config)); return page }
        for (rule in snapshot.rules) {
            val row = page.row(group, rule.match, rule.action + if (rule.outbound.isNotEmpty()) " → ${rule.outbound}" else "", R.drawable.ic_route, navigates = false) { editor(rule.order - 1) }
            row.label.maxLines = 2
            row.label.ellipsize = android.text.TextUtils.TruncateAt.END
            row.actionIcon(R.drawable.ic_edit, getString(R.string.edit_rule)) { editor(rule.order - 1) }
            lateinit var more: View
            fun menu() {
                PopupMenu(requireContext(), more).apply {
                    if (rule.order > 1) menu.add(0, 1, 0, R.string.rule_move_up)
                    if (rule.order < snapshot.rules.size) menu.add(0, 2, 1, R.string.rule_move_down)
                    menu.add(0, 3, 2, R.string.remove)
                    setOnMenuItemClickListener { item ->
                        try {
                            val latest = ConfigRepository.load(requireContext()).generatedContent
                            check(latest == content) { getString(R.string.editor_conflict) }
                            when (item.itemId) {
                                1 -> save(ConfigEdits.moveRule(latest, rule.order - 1, rule.order - 2))
                                2 -> save(ConfigEdits.moveRule(latest, rule.order - 1, rule.order))
                                3 -> {
                                    val after = ConfigEdits.routeRule(latest, rule.order - 1, null)
                                    save(after)
                                    view?.let { anchor -> Snackbar.make(anchor, R.string.rule_removed, Snackbar.LENGTH_LONG)
                                        .setAction(R.string.undo) {
                                            if (ConfigRepository.load(requireContext()).generatedContent == after) runCatching { save(latest) }
                                                .onFailure { Snackbar.make(anchor, R.string.edit_failed, Snackbar.LENGTH_LONG).show() }
                                            else Snackbar.make(anchor, R.string.editor_conflict, Snackbar.LENGTH_LONG).show()
                                        }.show() }
                                }
                            }
                        } catch (error: Exception) { view?.let { Snackbar.make(it, error.message ?: getString(R.string.invalid_value), Snackbar.LENGTH_LONG).show() } }
                        true
                    }
                }.show()
            }
            more = row.actionIcon(R.drawable.ic_more, getString(R.string.row_actions)) { menu() }
            row.setOnLongClickListener { menu(); true }
        }
        page.row(group, getString(R.string.policy_final), snapshot.final, R.drawable.ic_route) {
            EditDialogs.choice(requireContext(), getString(R.string.policy_final), ConfigEdits.outboundTags(content), snapshot.final) {
                save(ConfigEdits.finalOutbound(ConfigRepository.load(requireContext()).generatedContent, it))
            }
        }
        page.note(getString(R.string.device_edits_note))
        return page
    }
}
