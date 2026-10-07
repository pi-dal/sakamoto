package com.pidal.sakamoto.ui

import android.os.Bundle
import android.text.InputType
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.TextView
import androidx.activity.OnBackPressedCallback
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.snackbar.Snackbar
import com.google.android.material.textfield.TextInputEditText
import com.google.android.material.textfield.TextInputLayout
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.ConfigEdits
import com.pidal.sakamoto.runtime.ConfigRepository
import org.json.JSONArray
import org.json.JSONObject

/**
 * Full-page editor with explicit save at the top right, dirty protection,
 * optimistic concurrency and protocol-grouped fields (server / auth /
 * security / transport / chain). Secrets are never restored into persistent
 * view state and every save rewrites only the leaves it owns.
 */
class EditorFragment : androidx.fragment.app.Fragment() {

    private var page: GroupedPage? = null
    private var specs: List<EditingProtocols.FieldSpec> = emptyList()
    private val inputs = linkedMapOf<String, TextInputLayout>()
    private val valueReaders = linkedMapOf<String, () -> String>()
    private val choices = linkedMapOf<String, String>()
    private val originals = linkedMapOf<String, String>()
    private var entryAvailable = false
    private var dirty = false
    private var finished = false
    private var errorNote: TextView? = null
    private var actionRows: LinearLayout? = null
    private lateinit var savedContent: String
    private var baselineDigest = ""

    private fun digest(content: String) = java.security.MessageDigest.getInstance("SHA-256")
        .digest(content.toByteArray()).joinToString("") { "%02x".format(it) }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext())
        this.page = page
        entryAvailable = false
        val kind = requireArguments().getString("kind")!!
        val tag = requireArguments().getString("tag").orEmpty()
        savedContent = ConfigRepository.load(requireContext()).generatedContent
        baselineDigest = state?.getString("baselineDigest") ?: digest(savedContent)
        dirty = state?.getBoolean("dirty") == true

        val root = runCatching { JSONObject(savedContent) }.getOrNull()
        if (root == null) { showMissing(page); return page.root }

        fun watcher(input: TextInputEditText) {
            input.addTextChangedListener(object : android.text.TextWatcher {
                override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
                override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) { dirty = true }
                override fun afterTextChanged(s: android.text.Editable?) {}
            })
        }

        fun field(group: LinearLayout, spec: EditingProtocols.FieldSpec, original: String) {
            originals[spec.key] = original
            val context = requireContext()
            val layout = TextInputLayout(context).apply {
                hint = if (spec.labelRes != 0) context.getString(spec.labelRes) else EditingProtocols.fallbackLabel(spec.key)
                boxBackgroundMode = TextInputLayout.BOX_BACKGROUND_OUTLINE
                if (spec.kind == EditingProtocols.Kind.SECRET) endIconMode = TextInputLayout.END_ICON_PASSWORD_TOGGLE
            }
            val input = TextInputEditText(layout.context).apply {
                setText(if (spec.kind == EditingProtocols.Kind.SECRET) original else state?.getString("field:${spec.key}") ?: original)
                isSingleLine = true
                inputType = when (spec.kind) {
                    EditingProtocols.Kind.SECRET -> InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
                    EditingProtocols.Kind.NUMBER -> InputType.TYPE_CLASS_NUMBER
                    else -> if (spec.key == "server") InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
                    else InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
                }
                if (spec.kind == EditingProtocols.Kind.SECRET) {
                    // Secrets stay out of Android's persistent view state too.
                    isSaveEnabled = false
                    if (android.os.Build.VERSION.SDK_INT >= 26) importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO
                }
            }
            watcher(input)
            layout.addView(input, LinearLayout.LayoutParams(-1, -2))
            group.addView(layout, LinearLayout.LayoutParams(-1, -2).apply {
                marginStart = page.dp(20); marginEnd = page.dp(20); topMargin = page.dp(12); bottomMargin = page.dp(4)
            })
            inputs[spec.key] = layout
            valueReaders[spec.key] = { input.text?.toString().orEmpty() }
        }

        fun displayLabels(vararg pairs: Pair<String, Int>): LinkedHashMap<String, String> {
            val map = LinkedHashMap<String, String>()
            for ((value, res) in pairs) map[value] = getString(res)
            return map
        }

        fun choice(group: LinearLayout, spec: EditingProtocols.FieldSpec?, key: String, title: CharSequence, display: LinkedHashMap<String, String>, original: String, onChange: (String) -> Unit = {}) {
            originals[key] = original
            choices[key] = state?.getString("choice:$key") ?: original
            lateinit var row: GroupedPage.Row
            fun label(value: String) = display[value] ?: value
            row = page.row(group, title, label(choices[key].orEmpty()), R.drawable.ic_route) {
                EditDialogs.choice(requireContext(), title.toString(), display, choices[key].orEmpty()) { next ->
                    choices[key] = next; row.detail(label(next)); dirty = true; onChange(next)
                }
            }
            if (spec != null) {
                valueReaders[spec.key] = { choices[key].orEmpty() }
            }
        }

        /** Text fields get near-field errors; booleans and enums read as On/Off and picker rows. */
        fun editable(group: LinearLayout, spec: EditingProtocols.FieldSpec, original: String) {
            val title = if (spec.labelRes != 0) getString(spec.labelRes) else EditingProtocols.fallbackLabel(spec.key)
            when (spec.kind) {
                EditingProtocols.Kind.BOOLEAN ->
                    choice(group, spec, spec.key, title,
                        displayLabels("true" to R.string.setting_on, "false" to R.string.setting_off), original)
                EditingProtocols.Kind.CHOICE -> {
                    val labels = LinkedHashMap<String, String>()
                    for (option in spec.options) labels[option] = option.ifEmpty { getString(R.string.ux_editing_option_none) }
                    // Preserve existing protocol extensions on a no-op save.
                    if (original.isNotEmpty() && original !in labels) labels[original] = original
                    choice(group, spec, spec.key, title, labels, original)
                }
                else -> field(group, spec, original)
            }
        }

        when (kind) {
            "outbound" -> {
                val outbound = findOutbound(root, tag) ?: run { showMissing(page); return page.root }
                specs = EditingProtocols.plan(outbound.optString("type"), outbound)
                val server = page.section(getString(R.string.ux_editing_section_server))
                page.row(server, getString(R.string.node_protocol), outbound.optString("type"), navigates = false)
                for (spec in specs.filter { it.section == EditingProtocols.Section.SERVER }) {
                    editable(server, spec, EditingProtocols.readField(outbound, spec))
                }
                val authSpecs = specs.filter { it.section == EditingProtocols.Section.AUTH }
                if (authSpecs.isNotEmpty()) {
                    val auth = page.section(getString(R.string.ux_editing_section_auth))
                    for (spec in authSpecs) editable(auth, spec, EditingProtocols.readField(outbound, spec))
                }
                val securitySpecs = specs.filter { it.section == EditingProtocols.Section.SECURITY }
                if (securitySpecs.isNotEmpty()) {
                    val security = page.section(getString(R.string.ux_editing_section_security))
                    for (spec in securitySpecs) editable(security, spec, EditingProtocols.readField(outbound, spec))
                }
                val transportSpecs = specs.filter { it.section == EditingProtocols.Section.TRANSPORT }
                if (transportSpecs.isNotEmpty()) {
                    val transport = page.section(getString(R.string.ux_editing_section_transport))
                    page.row(transport, getString(R.string.ux_editing_transport_type),
                        outbound.optJSONObject("transport")?.optString("type").orEmpty(), navigates = false)
                    for (spec in transportSpecs) editable(transport, spec, EditingProtocols.readField(outbound, spec))
                }
                val chain = page.section(getString(R.string.ux_editing_section_chain))
                val detourLabels = displayLabels("" to R.string.proxy_direct_dial)
                for (candidate in ConfigEdits.outboundTags(savedContent)) if (candidate != tag) detourLabels[candidate] = candidate
                choice(chain, null, "detour", getString(R.string.proxy_chain_path), detourLabels, outbound.optString("detour"))
            }
            "rule" -> {
                val index = requireArguments().getInt("index", -1)
                // Same tolerance as the outbound branch: a saved config without
                // a route object shows the missing-entry note, never a crash.
                val route = root.optJSONObject("route") ?: run { showMissing(page); return page.root }
                val rules = route.optJSONArray("rules")
                val rule = when {
                    index < 0 -> JSONObject()
                    rules != null && index < rules.length() -> rules.optJSONObject(index)
                    else -> null
                } ?: run { showMissing(page); return page.root }
                val group = page.section(getString(R.string.ux_editing_section_match))
                val predicate = EditingRules.predicates.firstOrNull { rule.has(it) } ?: "domain"
                val predicateLabels = displayLabels(
                    "domain" to R.string.ux_editing_pred_domain,
                    "domain_suffix" to R.string.ux_editing_pred_domain_suffix,
                    "domain_keyword" to R.string.ux_editing_pred_domain_keyword,
                    "ip_cidr" to R.string.ux_editing_pred_ip_cidr,
                    "rule_set" to R.string.ux_editing_pred_rule_set,
                    "protocol" to R.string.ux_editing_pred_protocol,
                    "clash_mode" to R.string.ux_editing_pred_clash_mode,
                    "network" to R.string.ux_editing_pred_network,
                    "port" to R.string.ux_editing_pred_port,
                    "ip_is_private" to R.string.ux_editing_pred_ip_is_private,
                )
                choices["predicate"] = state?.getString("choice:predicate") ?: predicate
                lateinit var predicateRow: GroupedPage.Row
                predicateRow = page.row(group, getString(R.string.rule_match_type), predicateLabels[choices["predicate"]] ?: predicate, R.drawable.ic_route) {
                    EditDialogs.choice(requireContext(), getString(R.string.rule_match_type), predicateLabels, choices["predicate"].orEmpty()) { next ->
                        choices["predicate"] = next
                        predicateRow.detail(predicateLabels[next] ?: next)
                        inputs["match"]?.hint = predicateLabels[next] ?: next
                        dirty = true
                    }
                }
                val value = EditingRules.readValues(rule, choices["predicate"] ?: predicate)
                val matchSpec = EditingProtocols.FieldSpec("match", R.string.rule_match_value, EditingProtocols.Section.SERVER, optional = true)
                specs += matchSpec
                field(group, matchSpec, value)
                val matchLayout = inputs["match"]
                matchLayout?.hint = predicateLabels[choices["predicate"]] ?: predicate
                matchLayout?.helperText = getString(R.string.rule_match_value)
                val actionLabels = LinkedHashMap<String, String>()
                for (action in EditingRules.actions) actionLabels[action] = action
                choices["action"] = state?.getString("choice:action") ?: rule.optString("action", "route").ifEmpty { "route" }
                choices["outbound"] = state?.getString("choice:outbound") ?: rule.optString("outbound").ifEmpty {
                    route.optString("final", "direct")
                }
                lateinit var actionRow: GroupedPage.Row
                actionRow = page.row(group, getString(R.string.policy_action_hint), choices["action"].orEmpty(), R.drawable.ic_route) {
                    EditDialogs.choice(requireContext(), getString(R.string.policy_action_hint), actionLabels, choices["action"].orEmpty()) { next ->
                        choices["action"] = next; actionRow.detail(next); dirty = true; rebuildActionRows()
                    }
                }
                actionRows = LinearLayout(requireContext()).apply { orientation = LinearLayout.VERTICAL }
                group.addView(actionRows, LinearLayout.LayoutParams(-1, -2))
                rebuildActionRows()
            }
        }
        entryAvailable = true
        errorNote = page.note("").apply {
            visibility = View.GONE
            setTextColor(com.google.android.material.color.MaterialColors.getColor(this, com.google.android.material.R.attr.colorError))
        }
        page.note(getString(R.string.editor_save_hint))
        return page.root
    }

    private fun findOutbound(root: JSONObject, tag: String): JSONObject? {
        val items = root.optJSONArray("outbounds") ?: return null
        return (0 until items.length()).mapNotNull { items.optJSONObject(it) }.firstOrNull { it.optString("tag") == tag }
    }

    private fun showMissing(page: GroupedPage) {
        page.section(getString(R.string.editor_fields))
            .addView(page.text(getString(R.string.ux_editing_error_missing_entry)).apply {
                setTextColor(com.google.android.material.color.MaterialColors.getColor(this, com.google.android.material.R.attr.colorError))
                setPadding(page.dp(20), page.dp(16), page.dp(20), page.dp(16))
            })
    }

    /** The outbound row only exists while the action is `route`. */
    private fun rebuildActionRows() {
        val container = actionRows ?: return
        container.removeAllViews()
        if (choices["action"] != "route") return
        val outboundLabels = LinkedHashMap<String, String>()
        for (candidate in ConfigEdits.outboundTags(savedContent)) outboundLabels[candidate] = candidate
        lateinit var row: GroupedPage.Row
        row = page!!.row(container, getString(R.string.data_outbound), choices["outbound"].orEmpty(), R.drawable.ic_route) {
            EditDialogs.choice(requireContext(), getString(R.string.data_outbound), outboundLabels, choices["outbound"].orEmpty()) { next ->
                choices["outbound"] = next; row.detail(next); dirty = true
            }
        }
    }

    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        requireActivity().onBackPressedDispatcher.addCallback(viewLifecycleOwner, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() { leave() }
        })
        (requireActivity() as MainActivity).addToolbarMenu(object : androidx.core.view.MenuProvider {
            override fun onCreateMenu(menu: android.view.Menu, inflater: android.view.MenuInflater) {
                menu.add(0, R.id.editor_save, 0, R.string.save).apply {
                    isEnabled = entryAvailable
                    setShowAsAction(android.view.MenuItem.SHOW_AS_ACTION_ALWAYS)
                }
            }
            override fun onMenuItemSelected(item: android.view.MenuItem): Boolean {
                if (item.itemId != R.id.editor_save) return false
                save(requireArguments().getString("kind")!!, requireArguments().getString("tag").orEmpty())
                return true
            }
        }, viewLifecycleOwner)
    }

    fun hasUnsavedChanges(): Boolean = dirty && !finished
    fun confirmLeaving(action: () -> Unit) {
        if (hasUnsavedChanges()) MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.editor_discard_title)
            .setMessage(R.string.editor_discard_message).setNegativeButton(R.string.editor_keep, null)
            .setPositiveButton(R.string.editor_discard) { _, _ -> finished = true; action() }.show()
        else action()
    }
    private fun leave() = confirmLeaving { parentFragmentManager.popBackStack() }

    private fun value(key: String) = valueReaders[key]?.invoke().orEmpty()

    private fun clearErrors() {
        for (layout in inputs.values) layout.error = null
        errorNote?.visibility = View.GONE
    }

    private fun fail(message: String?) {
        errorNote?.let { it.text = message ?: getString(R.string.invalid_value); it.visibility = View.VISIBLE }
    }

    private fun showFieldError(key: String, message: CharSequence?) {
        val layout = inputs[key]
        if (layout != null) layout.error = message ?: getString(R.string.invalid_value)
        else fail(message?.toString() ?: getString(R.string.invalid_value))
    }

    /** Validates one editable leaf; errors land in the field's own slot. */
    private fun validate(spec: EditingProtocols.FieldSpec): Boolean {
        val raw = value(spec.key).trim()
        fun reject(message: Int): Boolean {
            showFieldError(spec.key, getString(message)); return false
        }
        if (raw.isEmpty()) {
            if (spec.optional || spec.key == "match") return true
            return reject(R.string.required_value)
        }
        when (spec.kind) {
            EditingProtocols.Kind.NUMBER -> {
                val number = raw.toLongOrNull() ?: return reject(R.string.ux_editing_error_number)
                if (spec.key == "server_port" && number !in 1..65535) return reject(R.string.ux_editing_error_port)
            }
            EditingProtocols.Kind.BOOLEAN -> if (raw !in setOf("true", "false")) return reject(R.string.ux_editing_error_bool)
            EditingProtocols.Kind.CHOICE -> if (raw !in spec.options) return reject(R.string.invalid_value)
            EditingProtocols.Kind.LIST -> if (EditingRules.entries(raw).isEmpty()) return reject(R.string.invalid_value)
            else -> {}
        }
        return true
    }

    private fun save(kind: String, tag: String) {
        if (!entryAvailable) return
        try {
            val current = ConfigRepository.load(requireContext())
            check(digest(current.generatedContent) == baselineDigest) { getString(R.string.editor_conflict) }
            val root = JSONObject(current.generatedContent)
            clearErrors()
            if (kind == "outbound") {
                val changedSpecs = specs.filter { value(it.key) != originals[it.key] }
                if (!changedSpecs.all { validate(it) }) return
                val detour = choices["detour"].orEmpty()
                if (detour.isNotEmpty()) ConfigEdits.validateDetour(root.toString(), tag, detour)
                val outbound = findOutbound(root, tag) ?: error(getString(R.string.ux_editing_error_missing_entry))
                EditingProtocols.applyChanges(outbound, specs, originals, specs.associate { it.key to value(it.key) })
                if (detour != originals["detour"]) {
                    if (detour.isEmpty()) outbound.remove("detour") else outbound.put("detour", detour)
                }
                persist(root.toString())
            } else {
                val index = requireArguments().getInt("index", -1)
                val rules = root.getJSONObject("route").optJSONArray("rules") ?: JSONArray()
                val rule = if (index < 0) JSONObject() else rules.getJSONObject(index)
                val predicate = choices["predicate"].orEmpty()
                val action = choices["action"].orEmpty()
                val built = try {
                    // The rule's current first known predicate is the slot the editor
                    // replaces; derive it at save time from the rule being written.
                    EditingRules.buildRule(rule, EditingRules.predicates.firstOrNull { rule.has(it) }, predicate, value("match"), action, choices["outbound"].orEmpty())
                } catch (error: IllegalArgumentException) {
                    showFieldError("match", error.message); return
                }
                persist(ConfigEdits.routeRule(root.toString(), if (index < 0) rules.length() else index, built))
            }
        } catch (error: Exception) { fail(error.message) }
    }

    private fun persist(content: String) {
        ConfigRepository.saveGeneratedEdit(requireContext(), content)
        finished = true
        parentFragmentManager.setFragmentResult("config-updated", Bundle())
        Snackbar.make(requireActivity().findViewById(R.id.fragment_container), R.string.saved_pending_apply, Snackbar.LENGTH_LONG).show()
        parentFragmentManager.popBackStack()
    }

    override fun onSaveInstanceState(out: Bundle) {
        out.putBoolean("dirty", dirty)
        out.putString("baselineDigest", baselineDigest)
        for (key in inputs.keys) {
            val spec = specs.firstOrNull { it.key == key }
            // Secrets are never written into persistent view state.
            if (spec?.kind != EditingProtocols.Kind.SECRET) {
                out.putString("field:$key", value(key))
            }
        }
        for ((key, value) in choices) out.putString("choice:$key", value)
        super.onSaveInstanceState(out)
    }

    override fun onDestroyView() {
        page = null; specs = emptyList(); inputs.clear(); valueReaders.clear()
        choices.clear(); originals.clear(); entryAvailable = false; errorNote = null; actionRows = null
        super.onDestroyView()
    }
}
