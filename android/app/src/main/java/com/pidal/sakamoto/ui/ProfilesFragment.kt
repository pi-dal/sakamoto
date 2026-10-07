package com.pidal.sakamoto.ui

import android.content.Context
import android.os.Bundle
import android.view.Gravity
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import androidx.appcompat.content.res.AppCompatResources
import androidx.core.view.ViewCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView
import com.google.android.material.color.MaterialColors
import com.google.android.material.snackbar.Snackbar
import com.google.android.material.tabs.TabLayout
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.runtime.ConfigEdits
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * NekoBox profile grammar: group tabs lead straight to the node list — no
 * stacked "Group controls" section above it. Search, node ordering and a
 * group latency test live in one compact control row. A row carries the
 * selection state on the left, name + protocol in the middle and the delay
 * result aligned at the trailing edge; server rows expose exactly one visible
 * edit action, nested groups navigate to their own tab, automatic groups
 * honestly refuse selection. Rows render through RecyclerView with keyed
 * diffing so live core updates never reconstruct the whole list.
 */
class ProfilesFragment : androidx.fragment.app.Fragment() {
    private var selectedGroup: String = ""
    private var filter = ""
    private var latencySort = false
    private var tabs: TabLayout? = null
    private var list: RecyclerView? = null
    private var emptyLabel: TextView? = null
    private var note: TextView? = null
    private var testButton: View? = null
    private var adapter: NodeAdapter? = null
    private var groups: List<ConfigRepository.Group> = emptyList()
    private var live = false
    private var tabTags: List<String> = emptyList()
    private var rebuilding = false

    /** Group whose rows are currently submitted; drives the scroll reset on group change. */
    private var renderedGroup: String? = null

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        selectedGroup = state?.getString("group") ?: arguments?.getString("group").orEmpty()
        filter = state?.getString("filter").orEmpty()
        latencySort = state?.getBoolean("latencySort") ?: false
        val root = LinearLayout(requireContext()).apply { orientation = LinearLayout.VERTICAL }

        tabs = TabLayout(requireContext()).apply { tabMode = TabLayout.MODE_SCROLLABLE }
        root.addView(tabs, LinearLayout.LayoutParams(-1, -2))

        root.addView(controlRow(root.context), LinearLayout.LayoutParams(-1, -2))

        val nodeAdapter = NodeAdapter(
            onSelect = ::selectNode,
            onEdit = ::editNode,
            onTest = ::testNode,
            onOpenGroup = ::openGroup,
            onAutoTap = { feedback(getString(R.string.profile_auto_group)) },
        )
        adapter = nodeAdapter
        val frame = FrameLayout(requireContext())
        list = RecyclerView(requireContext()).apply {
            layoutManager = LinearLayoutManager(requireContext())
            adapter = nodeAdapter
        }
        frame.addView(list, FrameLayout.LayoutParams(-1, -1))
        emptyLabel = TextView(requireContext()).apply {
            setTextAppearance(com.google.android.material.R.style.TextAppearance_Material3_BodyMedium)
            setTextColor(context.getColor(R.color.on_surface_variant))
            gravity = Gravity.CENTER
            setPadding(dp(40), dp(40), dp(40), dp(40))
            visibility = View.GONE
        }
        frame.addView(emptyLabel, FrameLayout.LayoutParams(-1, -2, Gravity.CENTER))
        root.addView(frame, LinearLayout.LayoutParams(-1, 0, 1f))

        note = TextView(requireContext()).apply {
            setTextAppearance(com.google.android.material.R.style.TextAppearance_Material3_BodySmall)
            setTextColor(context.getColor(R.color.on_surface_variant))
            setPadding(dp(20), dp(4), dp(20), dp(16))
        }
        root.addView(note, LinearLayout.LayoutParams(-1, -2))

        tabs?.addOnTabSelectedListener(object : TabLayout.OnTabSelectedListener {
            override fun onTabSelected(tab: TabLayout.Tab) {
                if (rebuilding) return
                // Tab text carries the "auto" suffix; the tag keeps the real group name.
                selectedGroup = tab.tag as? String ?: tab.text.toString()
                renderList()
            }
            override fun onTabUnselected(tab: TabLayout.Tab) {}
            // Telegram grammar: re-tapping the active tab returns to the top of the list.
            override fun onTabReselected(tab: TabLayout.Tab) { list?.scrollToPosition(0) }
        })
        refresh(CommandClientRuntime.groups.value)
        return root
    }

    /** One compact control row: search, node ordering, group latency test. */
    private fun controlRow(context: Context): View {
        val row = LinearLayout(context).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(8), dp(4), dp(4), dp(4))
        }
        row.addView(androidx.appcompat.widget.SearchView(context).apply {
            setIconifiedByDefault(false)
            maxWidth = Int.MAX_VALUE
            queryHint = getString(R.string.profile_search)
            setQuery(filter, false)
            setOnQueryTextListener(object : androidx.appcompat.widget.SearchView.OnQueryTextListener {
                override fun onQueryTextSubmit(query: String?) = true
                override fun onQueryTextChange(query: String?): Boolean { filter = query.orEmpty(); renderList(); return true }
            })
        }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        row.addView(controlIconButton(context, R.drawable.ux_profiles_sort, getString(R.string.profile_sort)) {
            val options = listOf(getString(R.string.profile_sort_original), getString(R.string.profile_sort_latency))
            EditDialogs.choice(requireContext(), getString(R.string.profile_sort), options, options[if (latencySort) 1 else 0]) {
                latencySort = it == options[1]; renderList()
            }
        })
        testButton = controlIconButton(context, R.drawable.ux_profiles_bolt, getString(R.string.profile_test_group)) {
            val group = groups.firstOrNull { it.tag == selectedGroup } ?: return@controlIconButton
            if (live) viewLifecycleOwner.lifecycleScope.launch(Dispatchers.IO) { CommandClientRuntime.urlTest(group.tag) }
        }
        row.addView(testButton)
        return row
    }

    private fun controlIconButton(context: Context, icon: Int, description: String, action: () -> Unit): View =
        androidx.appcompat.widget.AppCompatImageButton(context).apply {
            setImageResource(icon)
            imageTintList = android.content.res.ColorStateList.valueOf(context.getColor(R.color.on_surface_variant))
            scaleType = ImageView.ScaleType.CENTER
            contentDescription = description
            val attr = android.util.TypedValue()
            context.theme.resolveAttribute(android.R.attr.selectableItemBackgroundBorderless, attr, true)
            background = AppCompatResources.getDrawable(context, attr.resourceId)
            setOnClickListener { action() }
            layoutParams = LinearLayout.LayoutParams(dp(48), dp(48))
        }

    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        parentFragmentManager.setFragmentResultListener("config-updated", viewLifecycleOwner) { _, _ -> refresh(CommandClientRuntime.groups.value) }
        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(Lifecycle.State.RESUMED) {
                launch { CommandClientRuntime.groups.collect { refresh(it) } }
                launch { MobilecoreRuntime.state.collect { refresh(CommandClientRuntime.groups.value) } }
                // Only rows crossing a wall-clock freshness boundary are rebound.
                launch {
                    while (true) {
                        kotlinx.coroutines.delay(5000)
                        renderList()
                    }
                }
            }
        }
    }

    private fun refresh(stream: List<ConfigRepository.Group>) {
        val nextLive = stream.isNotEmpty() && MobilecoreRuntime.state.value.serviceState == "Running"
        val next = if (nextLive) stream else ConfigRepository.savedGroups(requireContext())
        if (next == groups && nextLive == live) return
        groups = next; live = nextLive
        if (selectedGroup !in next.map { it.tag }) selectedGroup = next.firstOrNull()?.tag.orEmpty()
        refreshTabs(next)
        renderList()
    }

    /** Tabs are rebuilt only when the set of groups actually changes. */
    private fun refreshTabs(next: List<ConfigRepository.Group>) {
        val tags = next.map { it.tag }
        if (tags == tabTags) return
        tabTags = tags
        val tabLayout = tabs ?: return
        rebuilding = true
        tabLayout.removeAllTabs()
        next.forEach { group ->
            val label = if (group.selectable) group.tag else group.tag + " " + getString(R.string.ux_profiles_auto_suffix)
            tabLayout.addTab(tabLayout.newTab().setText(label).apply { tag = group.tag }, group.tag == selectedGroup)
        }
        rebuilding = false
    }

    /** Submits the visible group's rows to the keyed adapter; unchanged rows are not rebound. */
    private fun renderList() {
        val currentAdapter = adapter ?: return
        val group = groups.firstOrNull { it.tag == selectedGroup }
        note?.text = when {
            group == null -> ""
            !group.selectable -> getString(R.string.profile_auto_group)
            live -> getString(R.string.profile_live_note)
            else -> getString(R.string.group_offline_note)
        }
        note?.visibility = if (group == null) View.GONE else View.VISIBLE
        testButton?.let {
            it.isEnabled = live
            it.alpha = if (live) 1f else 0.38f
        }
        val memberCounts = groups.associate { it.tag to it.items.size }
        val rows = if (group == null) emptyList() else {
            var items = group.items.distinctBy { it.tag }.filter { it.tag.contains(filter, true) || it.type.contains(filter, true) }
            if (latencySort) items = items.sortedWith(compareBy<ConfigRepository.GroupItem> { if (it.delay > 0) it.delay else Int.MAX_VALUE }.thenBy { it.tag })
            items.map {
                NodeRow(
                    tag = it.tag, type = it.type, delay = it.delay, testedAt = it.testedAt,
                    selected = it.tag == group.selected, selectable = group.selectable,
                    isGroup = it.type in GROUP_TYPES, members = memberCounts[it.tag] ?: 0,
                    stale = it.testedAt > 0 && System.currentTimeMillis() / 1000 - it.testedAt !in 0..FRESH_SECONDS,
                )
            }
        }
        emptyLabel?.apply {
            text = getString(if (group == null) R.string.profile_empty else R.string.profile_no_match)
            visibility = if (rows.isEmpty()) View.VISIBLE else View.GONE
        }
        currentAdapter.submitList(rows)
        // Same-group updates keep the scroll position via keyed diffing; a group
        // (tab) switch starts at the top like NekoBox.
        if (renderedGroup != selectedGroup) {
            renderedGroup = selectedGroup
            list?.scrollToPosition(0)
        }
    }

    // --- Row actions ---------------------------------------------------------

    private fun selectNode(row: NodeRow) {
        viewLifecycleOwner.lifecycleScope.launch {
            try {
                if (live) check(withContext(Dispatchers.IO) { CommandClientRuntime.selectOutbound(selectedGroup, row.tag) }) { MobilecoreRuntime.state.value.notice ?: getString(R.string.edit_failed) }
                else {
                    val saved = ConfigRepository.load(requireContext())
                    ConfigRepository.saveGeneratedEdit(requireContext(), ConfigEdits.groupDefault(saved.generatedContent, selectedGroup, row.tag))
                    refresh(emptyList())
                }
                feedback(getString(if (live) R.string.profile_selection_requested else R.string.saved_pending_apply))
            } catch (error: Exception) { feedback(error.message ?: getString(R.string.invalid_value)) }
        }
    }

    private fun editNode(row: NodeRow) {
        (requireActivity() as MainActivity).openChild(EditorFragment().apply {
            arguments = Bundle().apply { putString("kind", "outbound"); putString("tag", row.tag) }
        }, getString(R.string.profile_edit_title))
    }

    /** Secondary latency test: long press a server row; offline asks to connect. */
    private fun testNode(row: NodeRow) {
        if (row.isGroup) return
        if (live) viewLifecycleOwner.lifecycleScope.launch(Dispatchers.IO) { CommandClientRuntime.urlTest(row.tag) }
        else feedback(getString(R.string.group_connect_hint))
    }

    private fun openGroup(row: NodeRow) {
        selectedGroup = row.tag
        val index = groups.indexOfFirst { it.tag == row.tag }
        if (index >= 0) tabs?.getTabAt(index)?.select()
        renderList()
    }

    private fun feedback(text: String) { view?.let { Snackbar.make(it, text, Snackbar.LENGTH_LONG).show() } }

    private fun dp(value: Int) = (value * resources.displayMetrics.density).toInt()

    override fun onSaveInstanceState(out: Bundle) { out.putString("group", selectedGroup); out.putString("filter", filter); out.putBoolean("latencySort", latencySort); super.onSaveInstanceState(out) }

    override fun onDestroyView() { tabs = null; list = null; emptyLabel = null; note = null; testButton = null; adapter = null; groups = emptyList(); tabTags = emptyList(); renderedGroup = null; rebuilding = false; super.onDestroyView() }

    companion object {
        /** Outbound types that are themselves groups: tapping navigates, not selects. */
        private val GROUP_TYPES = setOf("selector", "urltest")

        /** Terminal/meta outbounds: no server settings to edit, no selection. */
        private val NON_SERVER_TYPES = setOf("direct", "block", "dns", "selector", "urltest")

        /** Same freshness window as the widget projection (WidgetPresentation). */
        private const val FRESH_SECONDS = 120
    }

    private data class NodeRow(
        val tag: String, val type: String, val delay: Int, val testedAt: Long,
        val selected: Boolean, val selectable: Boolean, val isGroup: Boolean, val members: Int,
        val stale: Boolean,
    )

    private inner class NodeAdapter(
        private val onSelect: (NodeRow) -> Unit,
        private val onEdit: (NodeRow) -> Unit,
        private val onTest: (NodeRow) -> Unit,
        private val onOpenGroup: (NodeRow) -> Unit,
        private val onAutoTap: () -> Unit,
    ) : ListAdapter<NodeRow, NodeHolder>(Diff) {
        override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): NodeHolder {
            // LinearLayoutManager's default item params are WRAP_CONTENT on both
            // axes; pin the row so body and divider always span the full width.
            val cell = Cell(parent.context, onSelect, onEdit, onTest, onOpenGroup, onAutoTap)
            cell.layoutParams = RecyclerView.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT)
            return NodeHolder(cell)
        }
        override fun onBindViewHolder(holder: NodeHolder, position: Int) {
            holder.bind(getItem(position))
        }
    }

    private object Diff : DiffUtil.ItemCallback<NodeRow>() {
        override fun areItemsTheSame(oldItem: NodeRow, newItem: NodeRow) = oldItem.tag == newItem.tag
        override fun areContentsTheSame(oldItem: NodeRow, newItem: NodeRow) = oldItem == newItem
    }

    /** Telegram-style status cell: left selection slot, name + protocol, right-aligned delay. */
    private class NodeHolder(cell: Cell) : RecyclerView.ViewHolder(cell) {
        fun bind(row: NodeRow) { (itemView as Cell).bind(row) }
    }

    private class Cell(
        context: Context,
        onSelect: (NodeRow) -> Unit,
        onEdit: (NodeRow) -> Unit,
        onTest: (NodeRow) -> Unit,
        onOpenGroup: (NodeRow) -> Unit,
        onAutoTap: () -> Unit,
    ) : LinearLayout(context) {
        private val status = ImageView(context)
        private val name = TextView(context)
        private val detail = TextView(context)
        private val delay = TextView(context)
        private val edit = androidx.appcompat.widget.AppCompatImageButton(context)
        private val chevron = ImageView(context)
        private var row: NodeRow? = null

        /** Press/ripple target: the body row, with the divider hanging below it. */
        private val rowContent = LinearLayout(context)

        init {
            orientation = VERTICAL
            rowContent.apply {
                orientation = HORIZONTAL
                gravity = Gravity.CENTER_VERTICAL
                minimumHeight = dp(72)
                setPaddingRelative(dp(20), dp(10), dp(12), dp(10))
                status.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
                status.scaleType = ImageView.ScaleType.CENTER
                status.setImageResource(R.drawable.ux_profiles_radio_off)
                addView(status, LayoutParams(dp(24), dp(24)).apply { marginEnd = dp(16) })

                val labels = LinearLayout(context).apply {
                    orientation = VERTICAL
                    name.setTextAppearance(com.google.android.material.R.style.TextAppearance_Material3_BodyLarge)
                    name.setTextColor(context.getColor(R.color.on_surface))
                    name.isSingleLine = true
                    name.ellipsize = android.text.TextUtils.TruncateAt.END
                    detail.setTextAppearance(com.google.android.material.R.style.TextAppearance_Material3_BodySmall)
                    detail.setTextColor(context.getColor(R.color.on_surface_variant))
                    detail.isSingleLine = true
                    detail.ellipsize = android.text.TextUtils.TruncateAt.END
                    addView(name, LayoutParams(-1, -2))
                    addView(detail, LayoutParams(-1, -2).apply { topMargin = dp(4) })
                }
                addView(labels, LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))

                delay.setTextAppearance(com.google.android.material.R.style.TextAppearance_Material3_BodySmall)
                delay.setTextColor(context.getColor(R.color.on_surface_variant))
                delay.maxLines = 2
                delay.gravity = Gravity.END
                delay.minWidth = dp(64)
                delay.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
                addView(delay, LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                    marginStart = dp(8)
                })

                edit.setImageResource(R.drawable.ic_edit)
                edit.imageTintList = android.content.res.ColorStateList.valueOf(context.getColor(R.color.on_surface_variant))
                edit.scaleType = ImageView.ScaleType.CENTER
                val editRipple = android.util.TypedValue()
                context.theme.resolveAttribute(android.R.attr.selectableItemBackgroundBorderless, editRipple, true)
                edit.background = AppCompatResources.getDrawable(context, editRipple.resourceId)
                addView(edit, LayoutParams(dp(48), dp(48)).apply { marginStart = dp(8) })

                chevron.setImageResource(R.drawable.ic_chevron)
                chevron.imageTintList = android.content.res.ColorStateList.valueOf(context.getColor(R.color.on_surface_variant))
                chevron.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
                if (resources.configuration.layoutDirection == View.LAYOUT_DIRECTION_RTL) chevron.scaleX = -1f
                addView(chevron, LayoutParams(dp(24), dp(24)).apply { marginStart = dp(8) })
            }
            addView(rowContent, LayoutParams(-1, ViewGroup.LayoutParams.WRAP_CONTENT))
            addView(View(context).apply { setBackgroundColor(context.getColor(R.color.divider)) },
                LayoutParams(-1, dp(1)).apply { marginStart = dp(20) })

            val attr = android.util.TypedValue()
            context.theme.resolveAttribute(android.R.attr.selectableItemBackground, attr, true)
            rowContent.foreground = AppCompatResources.getDrawable(context, attr.resourceId)
            rowContent.isFocusable = true
            rowContent.setOnClickListener {
                val target = row ?: return@setOnClickListener
                when {
                    target.isGroup -> onOpenGroup(target)
                    target.selectable -> onSelect(target)
                    else -> onAutoTap()
                }
            }
            rowContent.setOnLongClickListener {
                val target = row ?: return@setOnLongClickListener false
                if (target.isGroup) return@setOnLongClickListener false
                onTest(target); true
            }
            edit.setOnClickListener {
                val target = row ?: return@setOnClickListener
                onEdit(target)
            }
        }
        fun dp(value: Int) = (value * context.resources.displayMetrics.density).toInt()

        fun bind(row: NodeRow) {
            this.row = row
            name.text = row.tag
            name.setTextColor(context.getColor(if (row.selected) R.color.primary else R.color.on_surface))
            detail.text = when {
                row.isGroup && row.members > 0 -> context.getString(R.string.ux_profiles_group_detail, row.members)
                row.isGroup -> context.getString(R.string.ux_profiles_group_detail_unlabeled)
                else -> row.type
            }
            status.setImageResource(if (row.selected) R.drawable.ic_selected else R.drawable.ux_profiles_radio_off)
            status.imageTintList = android.content.res.ColorStateList.valueOf(context.getColor(
                if (row.selected) R.color.primary else R.color.on_surface_variant
            ))
            if (row.isGroup) {
                // Groups navigate: a routing glyph, never a selection radio.
                status.setImageResource(R.drawable.ic_route)
                status.imageTintList = android.content.res.ColorStateList.valueOf(context.getColor(R.color.on_surface_variant))
                status.visibility = View.VISIBLE
            } else if (row.selected) {
                status.setImageResource(R.drawable.ic_selected)
                status.imageTintList = android.content.res.ColorStateList.valueOf(context.getColor(R.color.primary))
                status.visibility = View.VISIBLE
            } else if (row.selectable) {
                status.setImageResource(R.drawable.ux_profiles_radio_off)
                status.imageTintList = android.content.res.ColorStateList.valueOf(context.getColor(R.color.on_surface_variant))
                status.visibility = View.VISIBLE
            } else {
                // Automatic groups: no ring on unselected rows — honest nonselectable affordance.
                status.visibility = View.INVISIBLE
            }

            val stale = row.stale
            val latencyWord: CharSequence
            if (row.isGroup) {
                delay.visibility = View.GONE
                latencyWord = detail.text
            } else {
                val errorColor = MaterialColors.getColor(this, com.google.android.material.R.attr.colorError)
                when {
                    row.testedAt > 0 && row.delay > 0 -> {
                        delay.text = context.getString(R.string.widget_latency_value, row.delay) +
                            if (stale) "\n" + context.getString(R.string.ux_profiles_stale_label) else ""
                        delay.setTextColor(context.getColor(if (stale) R.color.on_surface_variant else R.color.primary))
                    }
                    row.testedAt > 0 -> {
                        delay.text = context.getString(R.string.widget_latency_failed) +
                            if (stale) "\n" + context.getString(R.string.ux_profiles_stale_label) else ""
                        delay.setTextColor(if (stale) context.getColor(R.color.on_surface_variant) else errorColor)
                    }
                    else -> {
                        delay.text = context.getString(R.string.widget_latency_untested)
                        delay.setTextColor(context.getColor(R.color.on_surface_variant))
                    }
                }
                delay.visibility = View.VISIBLE
                latencyWord = when {
                    row.testedAt > 0 && row.delay > 0 && stale -> context.getString(R.string.ux_profiles_state_stale, row.delay)
                    row.testedAt > 0 && row.delay > 0 -> context.getString(R.string.widget_latency_value, row.delay)
                    row.testedAt > 0 -> context.getString(R.string.widget_latency_failed)
                    else -> context.getString(R.string.widget_latency_untested)
                }
            }
            edit.visibility = if (row.isGroup || row.type in ProfilesFragment.NON_SERVER_TYPES) View.GONE else View.VISIBLE
            edit.contentDescription = context.getString(R.string.profile_edit_node, row.tag)
            chevron.visibility = if (row.isGroup) View.VISIBLE else View.GONE
            ViewCompat.setStateDescription(rowContent, buildString {
                append(context.getString(if (row.selected) R.string.ux_profiles_state_selected else R.string.profile_not_selected))
                append(", ")
                append(latencyWord)
            })
        }
    }
}
