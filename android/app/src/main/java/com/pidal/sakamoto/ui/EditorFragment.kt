package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.activity.OnBackPressedCallback
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.snackbar.Snackbar
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.ConfigRepository
import org.json.JSONObject

/** Full-page editor with explicit save/cancel, draft restore and discard protection. */
class EditorFragment : androidx.fragment.app.Fragment() {
    private val inputs = linkedMapOf<String, com.google.android.material.textfield.TextInputEditText>()
    private val choices = linkedMapOf<String, String>()
    private val originals = linkedMapOf<String, String>()
    private var dirty = false
    private var finished = false
    private var errorView: android.widget.TextView? = null
    private lateinit var savedContent: String
    private var baselineDigest = ""
    private fun digest(content: String) = java.security.MessageDigest.getInstance("SHA-256").digest(content.toByteArray()).joinToString("") { "%02x".format(it) }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext())
        val kind = requireArguments().getString("kind")!!
        val tag = requireArguments().getString("tag").orEmpty()
        savedContent = ConfigRepository.load(requireContext()).generatedContent
        baselineDigest = state?.getString("baselineDigest") ?: digest(savedContent)
        val root = JSONObject(savedContent)
        val fields = page.section(getString(R.string.editor_fields))
        fun field(key: String, title: String, value: String, secret: Boolean = false) {
            originals[key] = value
            val input = page.field(fields, title, if (secret) value else state?.getString("field:$key") ?: value, secret = secret)
            inputs[key] = input
            input.addTextChangedListener(object : android.text.TextWatcher {
                override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
                override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) { dirty = true }
                override fun afterTextChanged(s: android.text.Editable?) {}
            })
        }
        fun choice(key: String, title: String, value: String, options: List<String>) {
            originals[key] = value
            choices[key] = state?.getString("choice:$key") ?: value
            lateinit var row: GroupedPage.Row
            row = page.row(fields, title, choices[key].orEmpty(), R.drawable.ic_route) {
                EditDialogs.choice(requireContext(), title, options, choices[key].orEmpty()) { next ->
                    choices[key] = next; row.detail(next); dirty = true
                }
            }
        }
        when (kind) {
            "outbound" -> {
                val items = root.getJSONArray("outbounds")
                val outbound = (0 until items.length()).map { items.getJSONObject(it) }.first { it.optString("tag") == tag }
                page.row(fields, getString(R.string.node_protocol), outbound.optString("type"))
                field("server", getString(R.string.proxy_server), outbound.optString("server"))
                field("server_port", getString(R.string.proxy_port), outbound.optString("server_port"))
                for (key in listOf("username", "password", "uuid")) {
                    if (outbound.has(key) || (outbound.optString("type") in setOf("socks", "http") && key != "uuid")) {
                        field(key, key.replaceFirstChar { it.uppercase() }, outbound.optString(key), secret = true)
                    }
                }
                choice("detour", getString(R.string.proxy_chain_path), outbound.optString("detour").ifEmpty { getString(R.string.proxy_direct_dial) },
                    listOf(getString(R.string.proxy_direct_dial)) + com.pidal.sakamoto.runtime.ConfigEdits.outboundTags(savedContent).filter { it != tag })
                outbound.optJSONObject("tls")?.let { tls ->
                    field("tls.server_name", getString(R.string.node_tls_name), tls.optString("server_name"))
                }
                page.note(getString(R.string.device_edits_note))
            }
            "rule" -> {
                val index = requireArguments().getInt("index", -1)
                val rule = if (index < 0) JSONObject() else root.getJSONObject("route").getJSONArray("rules").getJSONObject(index)
                val known = listOf("domain", "domain_suffix", "domain_keyword", "ip_cidr", "rule_set", "protocol", "clash_mode", "network", "port", "ip_is_private")
                val predicate = known.firstOrNull { rule.has(it) } ?: "domain"
                choice("predicate", getString(R.string.rule_match_type), predicate, known)
                val raw = rule.opt(predicate)
                val value = if (raw is org.json.JSONArray) (0 until raw.length()).joinToString(", ") { raw.get(it).toString() } else raw?.toString().orEmpty()
                field("match", getString(R.string.rule_match_value), value)
                choice("action", getString(R.string.policy_action_hint), rule.optString("action", "route"), listOf("route", "reject", "hijack-dns", "sniff", "resolve"))
                choice("outbound", getString(R.string.data_outbound), rule.optString("outbound").ifEmpty { root.getJSONObject("route").optString("final", "direct") }, com.pidal.sakamoto.runtime.ConfigEdits.outboundTags(savedContent))
                page.note(getString(R.string.rule_editor_note))
            }
        }
        errorView = page.note("").apply { visibility = View.GONE; setTextColor(com.google.android.material.color.MaterialColors.getColor(this, com.google.android.material.R.attr.colorError)) }
        page.note(getString(R.string.editor_save_hint))
        dirty = state?.getBoolean("dirty") == true
        return page.root
    }

    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        requireActivity().onBackPressedDispatcher.addCallback(viewLifecycleOwner, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() { leave() }
        })
        (requireActivity() as com.pidal.sakamoto.MainActivity).addToolbarMenu(object : androidx.core.view.MenuProvider {
            override fun onCreateMenu(menu: android.view.Menu, inflater: android.view.MenuInflater) {
                menu.add(0, R.id.editor_save, 0, R.string.save).setShowAsAction(android.view.MenuItem.SHOW_AS_ACTION_ALWAYS)
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

    private fun save(kind: String, tag: String) {
        try {
            val current = ConfigRepository.load(requireContext())
            check(digest(current.generatedContent) == baselineDigest) { getString(R.string.editor_conflict) }
            val root = JSONObject(current.generatedContent)
            fun value(key: String) = inputs[key]?.text?.toString() ?: choices[key].orEmpty()
            val next = if (kind == "outbound") {
                val array = root.getJSONArray("outbounds")
                val outbound = (0 until array.length()).map { array.getJSONObject(it) }.first { it.optString("tag") == tag }
                require(value("server").isNotBlank()) { getString(R.string.required_value) }
                val port = value("server_port").toIntOrNull() ?: error(getString(R.string.invalid_value))
                require(port in 1..65535) { getString(R.string.invalid_value) }
                val detour = value("detour").let { if (it == getString(R.string.proxy_direct_dial)) "" else it }
                com.pidal.sakamoto.runtime.ConfigEdits.validateDetour(root.toString(), tag, detour)
                outbound.put("server", value("server").trim()).put("server_port", port)
                if (detour.isEmpty()) outbound.remove("detour") else outbound.put("detour", detour)
                for (key in listOf("username", "password", "uuid")) if (inputs.containsKey(key)) outbound.put(key, value(key))
                if (inputs.containsKey("tls.server_name")) outbound.getJSONObject("tls").put("server_name", value("tls.server_name").trim())
                root.toString()
            } else {
                val index = requireArguments().getInt("index", -1)
                val rules = root.getJSONObject("route").getJSONArray("rules")
                val rule = if (index < 0) JSONObject() else JSONObject(rules.getJSONObject(index).toString())
                val oldPredicate = originals["predicate"]!!
                rule.remove(oldPredicate)
                val predicate = value("predicate")
                val entries = value("match").split(',').map { it.trim() }.filter { it.isNotEmpty() }
                val action = value("action")
                require(entries.isNotEmpty() || action in setOf("sniff", "resolve")) { getString(R.string.required_value) }
                if (entries.isNotEmpty()) {
                    when (predicate) {
                        "ip_is_private" -> { require(entries.single() in setOf("true", "false")); rule.put(predicate, entries.single().toBoolean()) }
                        "clash_mode" -> rule.put(predicate, entries.single())
                        "port" -> rule.put(predicate, org.json.JSONArray(entries.map { it.toInt().also { port -> require(port in 1..65535) } }))
                        else -> rule.put(predicate, org.json.JSONArray(entries))
                    }
                }
                rule.put("action", action)
                if (action == "route") rule.put("outbound", value("outbound")) else rule.remove("outbound")
                com.pidal.sakamoto.runtime.ConfigEdits.routeRule(root.toString(), if (index < 0) rules.length() else index, rule)
            }
            ConfigRepository.saveGeneratedEdit(requireContext(), next)
            finished = true
            parentFragmentManager.setFragmentResult("config-updated", Bundle())
            Snackbar.make(requireActivity().findViewById(R.id.fragment_container), R.string.saved_pending_apply, Snackbar.LENGTH_LONG).show()
            parentFragmentManager.popBackStack()
        } catch (error: Exception) {
            errorView?.text = error.message ?: getString(R.string.invalid_value)
            errorView?.visibility = View.VISIBLE
        }
    }

    override fun onSaveInstanceState(out: Bundle) {
        out.putBoolean("dirty", dirty)
        out.putString("baselineDigest", baselineDigest)
        inputs.filterKeys { it !in setOf("username", "password", "uuid") }.forEach { (key, input) -> out.putString("field:$key", input.text.toString()) }
        choices.forEach { (key, value) -> out.putString("choice:$key", value) }
        super.onSaveInstanceState(out)
    }
    override fun onDestroyView() { inputs.clear(); choices.clear(); originals.clear(); errorView = null; super.onDestroyView() }
}
