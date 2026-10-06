package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.LinearLayout
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import com.google.android.material.snackbar.Snackbar
import com.google.android.material.tabs.TabLayout
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.runtime.ConfigEdits
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import kotlinx.coroutines.launch
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** NekoBox interaction grammar: group tabs, tap selects, separate edit and menu. */
class ProfilesFragment : androidx.fragment.app.Fragment() {
    private var selectedGroup: String = ""
    private var filter = ""
    private var latencySort = false
    private var tabs: TabLayout? = null
    private var list: LinearLayout? = null
    private var groups: List<ConfigRepository.Group> = emptyList()
    private var live = false
    private var rebuilding = false

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        selectedGroup = state?.getString("group") ?: arguments?.getString("group").orEmpty()
        filter = state?.getString("filter").orEmpty()
        latencySort = state?.getBoolean("latencySort") ?: false
        val root = LinearLayout(requireContext()).apply { orientation = LinearLayout.VERTICAL }
        tabs = TabLayout(requireContext()).apply { tabMode = TabLayout.MODE_SCROLLABLE }
        root.addView(tabs)
        val search = androidx.appcompat.widget.SearchView(requireContext()).apply {
            setIconifiedByDefault(false); queryHint = getString(R.string.profile_search)
            setQuery(filter, false)
            setOnQueryTextListener(object : androidx.appcompat.widget.SearchView.OnQueryTextListener {
                override fun onQueryTextSubmit(query: String?) = true
                override fun onQueryTextChange(query: String?): Boolean { filter = query.orEmpty(); renderList(); return true }
            })
        }
        root.addView(search)
        val scroll = androidx.core.widget.NestedScrollView(requireContext())
        list = LinearLayout(requireContext()).apply { orientation = LinearLayout.VERTICAL }
        scroll.addView(requireNotNull(list))
        root.addView(scroll, LinearLayout.LayoutParams(-1, 0, 1f))
        tabs?.addOnTabSelectedListener(object : TabLayout.OnTabSelectedListener {
            override fun onTabSelected(tab: TabLayout.Tab) { if (!rebuilding) { selectedGroup = tab.text.toString(); renderList() } }
            override fun onTabUnselected(tab: TabLayout.Tab) {}
            override fun onTabReselected(tab: TabLayout.Tab) {}
        })
        refresh(CommandClientRuntime.groups.value)
        return root
    }

    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        parentFragmentManager.setFragmentResultListener("config-updated", viewLifecycleOwner) { _, _ -> refresh(CommandClientRuntime.groups.value) }
        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(Lifecycle.State.RESUMED) {
                launch { CommandClientRuntime.groups.collect { refresh(it) } }
                launch { MobilecoreRuntime.state.collect { refresh(CommandClientRuntime.groups.value) } }
            }
        }
    }
    private fun refresh(stream: List<ConfigRepository.Group>) {
        val nextLive = stream.isNotEmpty() && MobilecoreRuntime.state.value.serviceState == "Running"
        val next = if (nextLive) stream else ConfigRepository.savedGroups(requireContext())
        if (groups == next && live == nextLive && tabs?.tabCount == next.size && list?.childCount != 0) return
        groups = next; live = nextLive
        if (selectedGroup !in next.map { it.tag }) selectedGroup = next.firstOrNull()?.tag.orEmpty()
        rebuilding = true
        tabs?.removeAllTabs()
        next.forEach { group -> tabs?.let { it.addTab(it.newTab().setText(group.tag), group.tag == selectedGroup) } }
        rebuilding = false
        renderList()
    }
    private fun renderList() {
        val container = list ?: return
        container.removeAllViews()
        val page = GroupedPage(requireContext())
        val group = groups.firstOrNull { it.tag == selectedGroup }
        if (group == null) page.row(page.section(getString(R.string.profiles_title)), getString(R.string.profile_empty))
        else {
            val actions = page.section(getString(R.string.profile_group_controls))
            page.row(actions, getString(if (group.selectable) R.string.group_selected else R.string.profile_automatic_selection), group.selected.ifEmpty { "—" })
            page.row(actions, getString(R.string.profile_sort), getString(if (latencySort) R.string.profile_sort_latency else R.string.profile_sort_original), R.drawable.ic_route) {
                val options = listOf(getString(R.string.profile_sort_original), getString(R.string.profile_sort_latency))
                EditDialogs.choice(requireContext(), getString(R.string.profile_sort), options, options[if (latencySort) 1 else 0]) {
                    latencySort = it == options[1]; renderList()
                }
            }
            page.button(actions, getString(R.string.profile_test_group)) {
                if (live) viewLifecycleOwner.lifecycleScope.launch(Dispatchers.IO) { CommandClientRuntime.urlTest(group.tag) }
                else feedback(getString(R.string.group_connect_hint))
            }.apply { isEnabled = live }
            val members = page.section(getString(R.string.profile_members))
            var items = group.items.filter { it.tag.contains(filter, true) || it.type.contains(filter, true) }
            if (latencySort) items = items.sortedWith(compareBy<ConfigRepository.GroupItem> { if (it.delay > 0) it.delay else Int.MAX_VALUE }.thenBy { it.tag })
            for (item in items) {
                val selected = item.tag == group.selected
                val row = page.row(members, item.tag, item.type + if (item.delay > 0) " · ${item.delay} ms" else "", R.drawable.ic_node, navigates = false) {
                    if (!group.selectable) feedback(getString(R.string.profile_auto_group))
                    else viewLifecycleOwner.lifecycleScope.launch {
                        try {
                            if (live) check(withContext(Dispatchers.IO) { CommandClientRuntime.selectOutbound(group.tag, item.tag) }) { MobilecoreRuntime.state.value.notice ?: getString(R.string.edit_failed) }
                            else {
                                val saved = ConfigRepository.load(requireContext())
                                ConfigRepository.saveGeneratedEdit(requireContext(), ConfigEdits.groupDefault(saved.generatedContent, group.tag, item.tag))
                                refresh(emptyList())
                            }
                            feedback(getString(if (live) R.string.profile_selection_requested else R.string.saved_pending_apply))
                        } catch (error: Exception) { feedback(error.message ?: getString(R.string.invalid_value)) }
                    }
                }
                row.selectedNode(selected)
                row.label.maxLines = 2
                row.label.ellipsize = android.text.TextUtils.TruncateAt.END
                if (item.type in setOf("selector", "urltest")) {
                    row.actionIcon(R.drawable.ic_chevron, getString(R.string.profile_open_group, item.tag)) {
                        selectedGroup = item.tag
                        tabs?.getTabAt(groups.indexOfFirst { it.tag == item.tag })?.select()
                        renderList()
                    }
                } else if (item.type !in setOf("direct", "block", "dns")) {
                    row.actionIcon(R.drawable.ic_edit, getString(R.string.profile_edit_node, item.tag)) {
                        (requireActivity() as MainActivity).openChild(EditorFragment().apply {
                            arguments = Bundle().apply { putString("kind", "outbound"); putString("tag", item.tag) }
                        }, getString(R.string.profile_edit_title))
                    }
                }
                row.actionIcon(R.drawable.ic_more, getString(R.string.profile_node_actions, item.tag)) { showActions(item) }
                row.setOnLongClickListener { showActions(item); true }
            }
            if (items.isEmpty()) page.row(members, getString(R.string.profile_no_match))
            page.note(getString(if (live) R.string.profile_live_note else R.string.group_offline_note))
        }
        val content = page.root.getChildAt(0)
        page.root.removeView(content); container.addView(content)
    }
    private fun showActions(item: ConfigRepository.GroupItem) {
        val dialog = com.google.android.material.bottomsheet.BottomSheetDialog(requireContext())
        val page = GroupedPage(requireContext())
        val group = page.section(item.tag)
        page.button(group, getString(R.string.url_test)) {
            dialog.dismiss()
            if (live) viewLifecycleOwner.lifecycleScope.launch(Dispatchers.IO) { CommandClientRuntime.urlTest(item.tag) }
            else feedback(getString(R.string.group_connect_hint))
        }
        page.button(group, getString(android.R.string.cancel)) { dialog.dismiss() }
        dialog.setContentView(page.root); dialog.show()
    }
    private fun feedback(text: String) { view?.let { Snackbar.make(it, text, Snackbar.LENGTH_LONG).show() } }
    override fun onSaveInstanceState(out: Bundle) { out.putString("group", selectedGroup); out.putString("filter", filter); out.putBoolean("latencySort", latencySort); super.onSaveInstanceState(out) }
    override fun onDestroyView() { tabs = null; list = null; groups = emptyList(); super.onDestroyView() }
}
