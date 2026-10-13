package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import androidx.activity.OnBackPressedCallback
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.snackbar.Snackbar
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.ProxyGroupEdit
import com.pidal.sakamoto.runtime.ProxyGroupEditing
import org.json.JSONObject

class ProxyGroupsFragment : androidx.fragment.app.Fragment() {
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View = FrameLayout(requireContext())
    override fun onResume() {
        super.onResume()
        val page = GroupedPage(requireContext())
        val groups = page.section(getString(R.string.proxy_groups_title))
        val content = ConfigRepository.load(requireContext()).generatedContent
        val rows = runCatching { ProxyGroupEditing.groups(content) }.getOrDefault(emptyList())
        for (group in rows) page.row(groups, group.tag,
            getString(R.string.group_summary, getString(if (group.type == "selector") R.string.group_manual else R.string.group_automatic), group.members.size), R.drawable.ic_node) {
            (requireActivity() as MainActivity).openChild(ProxyGroupEditorFragment().apply { arguments = Bundle().apply { putString("tag", group.tag) } }, getString(R.string.group_edit))
        }
        if (content.isBlank()) page.paragraph(groups, getString(R.string.group_config_required))
        else page.button(groups, getString(R.string.group_add)) {
            (requireActivity() as MainActivity).openChild(ProxyGroupEditorFragment(), getString(R.string.group_add))
        }
        page.note(getString(R.string.group_save_note))
        (view as? FrameLayout)?.apply { removeAllViews(); addView(page.root) }
    }
}

class ProxyGroupEditorFragment : androidx.fragment.app.Fragment(), UnsavedConfigEditor {
    private var baseline = ""
    private var originalTag = ""
    private var dirty = false
    private var finished = false
    class Draft : androidx.lifecycle.ViewModel() { var edit: ProxyGroupEdit? = null }
    private lateinit var edit: ProxyGroupEdit
    private var selected: android.widget.LinearLayout? = null
    private var available: android.widget.LinearLayout? = null
    private var behavior: android.widget.LinearLayout? = null
    private var error: android.widget.TextView? = null
    private var page: GroupedPage? = null
    private var query = ""
    private var selectedLimit = 100
    private var availableLimit = 100
    private var candidates: List<Pair<String, String>> = emptyList()
    private fun digest(value: String) = java.security.MessageDigest.getInstance("SHA-256").digest(value.toByteArray()).joinToString("") { "%02x".format(it) }
    private var baselineDigest = ""
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext()); this.page = page
        baseline = ConfigRepository.load(requireContext()).generatedContent
        baselineDigest = state?.getString("digest") ?: digest(baseline)
        originalTag = arguments?.getString("tag").orEmpty()
        edit = runCatching { ProxyGroupEditing.groups(baseline).firstOrNull { it.tag == originalTag } }.getOrNull() ?: ProxyGroupEdit("")
        if (originalTag.isNotEmpty() && edit.tag.isEmpty()) { page.note(getString(R.string.editor_conflict)); return page.root }
        edit = androidx.lifecycle.ViewModelProvider(this)[Draft::class.java].edit ?: edit.copy(tag = state?.getString("tag") ?: edit.tag, type = state?.getString("type") ?: edit.type,
            members = state?.getStringArrayList("members") ?: edit.members, defaultMember = state?.getString("default") ?: edit.defaultMember,
            interval = state?.getString("interval") ?: edit.interval)
        dirty = state?.getBoolean("dirty") ?: false
        val root = runCatching { JSONObject(baseline) }.getOrNull()
        val endpoints = root?.optJSONArray("endpoints")
        val endpointRows = if (endpoints == null) emptyList() else (0 until endpoints.length()).map { endpoints.getJSONObject(it) }
        candidates = (runCatching { ProxyGroupEditing.rows(baseline) }.getOrDefault(emptyList()) + endpointRows).map { it.getString("tag") to it.optString("type") }.filter { it.first != originalTag }
        val settings = page.section(getString(R.string.group_settings))
        if (originalTag.isEmpty()) {
            page.field(settings, getString(R.string.group_name), edit.tag).apply {
                addTextChangedListener(object : android.text.TextWatcher {
                    override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
                    override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) { edit = edit.copy(tag = s.toString()); dirty = true }
                    override fun afterTextChanged(s: android.text.Editable?) {}
                })
            }
        } else page.row(settings, getString(R.string.group_name), edit.tag, navigates = false)
        lateinit var modeRow: GroupedPage.Row
        val modes = linkedMapOf("selector" to getString(R.string.group_manual), "urltest" to getString(R.string.group_automatic))
        modeRow = page.row(settings, getString(R.string.group_selection), modes[edit.type].orEmpty()) {
            EditDialogs.choice(requireContext(), getString(R.string.group_selection), modes, edit.type) { type -> edit = edit.copy(type = type); dirty = true; modeRow.detail(modes[type].orEmpty()); renderBehavior() }
        }
        behavior = android.widget.LinearLayout(requireContext()).apply { orientation = android.widget.LinearLayout.VERTICAL; settings.addView(this) }
        renderBehavior()
        selected = page.section(getString(R.string.group_selected_members))
        page.note(getString(R.string.group_order_note))
        val find = page.section(getString(R.string.group_choose_members))
        page.field(find, getString(R.string.group_search)).apply {
            addTextChangedListener(object : android.text.TextWatcher {
                override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
                override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) { query = s.toString(); availableLimit = 100; renderMembers() }
                override fun afterTextChanged(s: android.text.Editable?) {}
            })
        }
        available = android.widget.LinearLayout(requireContext()).apply { orientation = android.widget.LinearLayout.VERTICAL; find.addView(this) }
        error = page.note("").apply { visibility = View.GONE }
        page.note(getString(R.string.group_save_note))
        renderMembers()
        return page.root
    }
    private fun renderBehavior() {
        val page = page ?: return; val group = behavior ?: return
        group.removeAllViews()
        if (edit.type == "selector") {
            page.row(group, getString(R.string.group_default), edit.defaultMember.ifEmpty { getString(R.string.group_first_member) }) {
                val labels = linkedMapOf("" to getString(R.string.group_first_member)); edit.members.forEach { labels[it] = it }
                EditDialogs.choice(requireContext(), getString(R.string.group_default), labels, edit.defaultMember) { value -> edit = edit.copy(defaultMember = value); dirty = true; renderBehavior() }
            }
        } else {
            page.row(group, getString(R.string.group_test_url), edit.url) {
                EditDialogs.text(requireContext(), getString(R.string.group_test_url), edit.url) { value -> edit = edit.copy(url = value.trim()); dirty = true; renderBehavior() }
            }
            page.row(group, getString(R.string.group_test_interval), edit.interval) {
                val values = (listOf("30s", "1m", "3m", "5m", "10m") + edit.interval).distinct()
                EditDialogs.choice(requireContext(), getString(R.string.group_test_interval), values, edit.interval) { value -> edit = edit.copy(interval = value); dirty = true; renderBehavior() }
            }
        }
    }
    private fun renderMembers() {
        val page = page ?: return
        selected?.let { group ->
            group.removeAllViews()
            if (edit.members.isEmpty()) page.paragraph(group, getString(R.string.group_empty_members))
            edit.members.take(selectedLimit).forEachIndexed { index, tag ->
                val row = page.row(group, tag, (index + 1).toString(), navigates = false)
                if (index > 0) row.actionIcon(R.drawable.ic_member_up, getString(R.string.group_move_up, tag)) { move(index, index - 1) }
                if (index < edit.members.lastIndex) row.actionIcon(R.drawable.ic_member_down, getString(R.string.group_move_down, tag)) { move(index, index + 1) }
                row.actionIcon(R.drawable.ux_profiles_delete, getString(R.string.group_remove_member, tag)) {
                    edit = edit.copy(members = edit.members - tag, defaultMember = edit.defaultMember.takeUnless { it == tag }.orEmpty()); dirty = true; renderMembers(); renderBehavior()
                }
            }
            if (edit.members.size > selectedLimit) page.button(group, getString(R.string.group_show_more, edit.members.size - selectedLimit)) { selectedLimit += 100; renderMembers() }
        }
        available?.let { group ->
            group.removeAllViews()
            val matches = candidates.filter { it.first !in edit.members && (query.isEmpty() || it.first.contains(query, true)) }
            if (matches.isEmpty()) page.paragraph(group, getString(R.string.group_no_matches))
            for ((tag, type) in matches.take(availableLimit)) page.row(group, tag, type, R.drawable.ic_node) {
                edit = edit.copy(members = edit.members + tag); dirty = true; renderMembers(); renderBehavior()
            }
            if (matches.size > availableLimit) page.button(group, getString(R.string.group_show_more, matches.size - availableLimit)) { availableLimit += 100; renderMembers() }
        }
    }
    private fun move(from: Int, to: Int) { val next = edit.members.toMutableList(); next.add(to, next.removeAt(from)); edit = edit.copy(members = next); dirty = true; renderMembers() }
    override fun onViewCreated(view: View, state: Bundle?) {
        (requireActivity() as MainActivity).addToolbarMenu(object : androidx.core.view.MenuProvider {
            override fun onCreateMenu(menu: android.view.Menu, inflater: android.view.MenuInflater) { menu.add(0, R.id.editor_save, 0, R.string.save).setShowAsAction(android.view.MenuItem.SHOW_AS_ACTION_ALWAYS) }
            override fun onMenuItemSelected(item: android.view.MenuItem): Boolean { if (item.itemId != R.id.editor_save) return false; save(); return true }
        }, viewLifecycleOwner)
        requireActivity().onBackPressedDispatcher.addCallback(viewLifecycleOwner, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                confirmLeaving { parentFragmentManager.popBackStack() }
            }
        })
    }
    override fun hasUnsavedChanges(): Boolean = dirty && !finished
    override fun confirmLeaving(action: () -> Unit) {
        if (hasUnsavedChanges()) MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.editor_discard_title).setMessage(R.string.editor_discard_message)
            .setNegativeButton(R.string.editor_keep, null).setPositiveButton(R.string.editor_discard) { _, _ -> finished = true; action() }.show()
        else action()
    }
    private fun save() {
        try {
            val current = ConfigRepository.load(requireContext())
            check(digest(current.generatedContent) == baselineDigest) { getString(R.string.editor_conflict) }
            edit = edit.copy(tag = edit.tag.trim())
            check(originalTag.isNotEmpty() || candidates.none { it.first == edit.tag }) { getString(R.string.group_duplicate_name) }
            ConfigRepository.saveGeneratedEdit(requireContext(), ProxyGroupEditing.apply(current.generatedContent, edit))
            finished = true
            parentFragmentManager.setFragmentResult("config-updated", Bundle())
            Snackbar.make(requireActivity().findViewById(R.id.fragment_container), R.string.saved_pending_apply, Snackbar.LENGTH_LONG).show()
            parentFragmentManager.popBackStack()
        } catch (exception: Exception) {
            val message = exception.message ?: getString(R.string.invalid_value)
            error?.apply { text = message; visibility = View.VISIBLE }
            view?.let { Snackbar.make(it, message, Snackbar.LENGTH_LONG).show() }
        }
    }
    override fun onSaveInstanceState(out: Bundle) {
        if (::edit.isInitialized) { out.putString("tag", edit.tag); out.putString("type", edit.type); out.putStringArrayList("members", ArrayList(edit.members)); out.putString("default", edit.defaultMember); out.putString("interval", edit.interval) }
        out.putString("digest", baselineDigest); out.putBoolean("dirty", dirty); super.onSaveInstanceState(out)
    }
    override fun onDestroyView() {
        if (::edit.isInitialized) androidx.lifecycle.ViewModelProvider(this)[Draft::class.java].edit = edit
        selected = null; available = null; behavior = null; error = null; page = null; super.onDestroyView()
    }
}
