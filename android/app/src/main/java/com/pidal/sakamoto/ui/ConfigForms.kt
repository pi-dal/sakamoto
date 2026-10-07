package com.pidal.sakamoto.ui

import android.os.Bundle
import android.text.InputType
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ArrayAdapter
import android.widget.AutoCompleteTextView
import android.widget.LinearLayout
import android.widget.TextView
import androidx.core.widget.NestedScrollView
import com.google.android.material.bottomsheet.BottomSheetBehavior
import com.google.android.material.bottomsheet.BottomSheetDialog
import com.google.android.material.bottomsheet.BottomSheetDialogFragment
import com.google.android.material.button.MaterialButton
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.textfield.TextInputEditText
import com.google.android.material.textfield.TextInputLayout
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.ConfigRepository
import io.nekohasekai.mobilecore.Mobilecore

private fun LinearLayout.sheetField(label: String, inputType: Int = InputType.TYPE_CLASS_TEXT): TextInputEditText {
    val field = TextInputEditText(context).apply { this.inputType = inputType; isSingleLine = true }
    addView(TextInputLayout(context).apply { hint = label; addView(field, LinearLayout.LayoutParams(-1, -2)) }, LinearLayout.LayoutParams(-1, -2).apply { topMargin = (12 * resources.displayMetrics.density).toInt() })
    return field
}

/** Source sheets open expanded with a title and explicit save/cancel; long
 *  forms scroll instead of growing past the screen. */
abstract class ConfigSheet : BottomSheetDialogFragment() {
    override fun onCreateDialog(savedInstanceState: Bundle?): android.app.Dialog {
        val dialog = super.onCreateDialog(savedInstanceState) as BottomSheetDialog
        // A form is a decision surface: it appears expanded, never half-open.
        dialog.behavior.skipCollapsed = true
        dialog.behavior.state = BottomSheetBehavior.STATE_EXPANDED
        return dialog
    }

    override fun onStart() {
        super.onStart()
        (dialog as? BottomSheetDialog)?.behavior?.apply {
            skipCollapsed = true
            state = BottomSheetBehavior.STATE_EXPANDED
        }
    }

    protected fun content(): LinearLayout = LinearLayout(requireContext()).apply {
        orientation = LinearLayout.VERTICAL
        val density = resources.displayMetrics.density
        setPadding((20 * density).toInt(), (16 * density).toInt(), (20 * density).toInt(), (24 * density).toInt())
    }

    protected fun sheetTitle(parent: LinearLayout, title: Int) {
        val density = resources.displayMetrics.density
        parent.addView(TextView(requireContext()).apply {
            setTextAppearance(com.google.android.material.R.style.TextAppearance_Material3_TitleMedium)
            setTextColor(context.getColor(R.color.on_surface))
            setText(title)
        }, LinearLayout.LayoutParams(-1, -2).apply { bottomMargin = (4 * density).toInt() })
    }

    protected fun scrollWrap(form: LinearLayout): View = NestedScrollView(requireContext()).apply {
        isFillViewport = true
        addView(form, ViewGroup.LayoutParams(-1, -2))
    }

    protected fun finishButton(parent: LinearLayout, text: Int, action: () -> Unit) {
        val density = resources.displayMetrics.density
        parent.addView(MaterialButton(requireContext()).apply { setText(text); minHeight = (48 * density).toInt(); setOnClickListener { action() } }, LinearLayout.LayoutParams(-1, -2).apply { topMargin = (20 * density).toInt() })
        parent.addView(MaterialButton(requireContext(), null, com.google.android.material.R.attr.borderlessButtonStyle).apply {
            setText(android.R.string.cancel); minHeight = (48 * density).toInt(); setOnClickListener { dismiss() }
        }, LinearLayout.LayoutParams(-1, -2).apply { topMargin = (4 * density).toInt() })
    }
}

class AddPolicySheet : ConfigSheet() {
    override fun onCreateView(i: LayoutInflater, c: ViewGroup?, s: Bundle?): View {
        val original = arguments?.getString("match")?.let { ConfigRepository.PolicyRule(it, arguments?.getString("action") ?: "proxy") }
        val root = content()
        sheetTitle(root, if (original == null) R.string.add_policy_action else R.string.edit_rule)
        val match = root.sheetField(getString(R.string.policy_match_hint))
        match.setText(original?.match.orEmpty())
        val action = AutoCompleteTextView(requireContext()).apply { setText("proxy"); setAdapter(ArrayAdapter(requireContext(), android.R.layout.simple_dropdown_item_1line, listOf("proxy", "direct", "reject"))); inputType = InputType.TYPE_NULL; setOnClickListener { showDropDown() } }
        action.setText(original?.action ?: "proxy", false)
        root.addView(TextInputLayout(requireContext()).apply { hint = getString(R.string.policy_action_hint); addView(action) }, LinearLayout.LayoutParams(-1, -2).apply { topMargin = 12 })
        finishButton(root, if (original == null) R.string.add else R.string.save) {
            try {
                val info = Mobilecore.normalizePolicyRule(match.text.toString(), action.text.toString())
                ConfigRepository.savePolicy(requireContext(), original, ConfigRepository.PolicyRule(info.match, info.action))
                parentFragmentManager.setFragmentResult("config-updated", Bundle())
                dismiss()
            } catch (e: Exception) { match.error = e.message ?: getString(R.string.invalid_value) }
        }
        return scrollWrap(root)
    }
}

class AddNodeSheet : ConfigSheet() {
    override fun onCreateView(i: LayoutInflater, c: ViewGroup?, s: Bundle?): View {
        val original = arguments?.getString("original")
        val root = content()
        sheetTitle(root, R.string.ux_editing_sheet_node_title)
        val link = root.sheetField(getString(R.string.node_link_hint), InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS)
        link.setText(original ?: arguments?.getString("prefill").orEmpty())
        link.isSaveEnabled = false
        if (android.os.Build.VERSION.SDK_INT >= 26) link.importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO
        finishButton(root, if (original == null) R.string.add else R.string.save) {
            try {
                val raw = link.text.toString().trim()
                check(raw.isNotEmpty()) { getString(R.string.required_value) }
                check(!Mobilecore.isSubLink(raw)) { getString(R.string.subscription_link_hint) }
                val info = Mobilecore.parseShareLink(raw); val current = ConfigRepository.load(requireContext())
                check(current.nodes.none { it == raw && it != original }) { getString(R.string.duplicate_node) }
                ConfigRepository.save(requireContext(), current.copy(sourceNeedsGenerate = true, nodes = if (original == null) current.nodes + raw else current.nodes.map { if (it == original) raw else it })); com.pidal.sakamoto.runtime.MobilecoreRuntime.configEvent("modified"); parentFragmentManager.setFragmentResult("config-updated", Bundle()); dismiss()
            } catch (e: Exception) { link.error = e.message ?: getString(R.string.invalid_value) }
        }
        return scrollWrap(root)
    }
}

class AddSubscriptionSheet : ConfigSheet() {
    override fun onCreateView(i: LayoutInflater, c: ViewGroup?, s: Bundle?): View {
        val originalName = arguments?.getString("name")
        val originalUrl = arguments?.getString("url")
        val root = content()
        sheetTitle(root, R.string.ux_editing_sheet_subscription_title)
        val name = root.sheetField(getString(R.string.subscription_name_hint)); val url = root.sheetField(getString(R.string.subscription_url_hint), InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI)
        name.setText(originalName.orEmpty()); url.setText(originalUrl ?: arguments?.getString("prefill").orEmpty()); url.isSaveEnabled = false
        if (android.os.Build.VERSION.SDK_INT >= 26) url.importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO
        val format = AutoCompleteTextView(requireContext()).apply { setText("auto"); setAdapter(ArrayAdapter(requireContext(), android.R.layout.simple_dropdown_item_1line, listOf("auto", "singbox", "clash", "base64"))); inputType = InputType.TYPE_NULL; setOnClickListener { showDropDown() } }
        format.setText(arguments?.getString("format") ?: "auto", false)
        root.addView(TextInputLayout(requireContext()).apply { hint = getString(R.string.subscription_format_hint); addView(format) }, LinearLayout.LayoutParams(-1, -2).apply { topMargin = 12 })
        finishButton(root, if (originalUrl == null) R.string.add else R.string.save) {
            try {
                val n = name.text.toString().trim(); val u = url.text.toString().trim(); check(n.isNotEmpty() && u.isNotEmpty()) { getString(R.string.required_value) }; check(u.startsWith("https://") || u.startsWith("http://")) { getString(R.string.import_error_scheme) }; Mobilecore.validateSourceURL(u)
                val current = ConfigRepository.load(requireContext()); ConfigRepository.save(requireContext(), current.copy(sourceNeedsGenerate = true, subscriptions = if (originalUrl == null) current.subscriptions + Triple(n, u, format.text.toString()) else current.subscriptions.map { if (it.first == originalName && it.second == originalUrl) Triple(n, u, format.text.toString()) else it })); com.pidal.sakamoto.runtime.MobilecoreRuntime.configEvent("modified"); parentFragmentManager.setFragmentResult("config-updated", Bundle()); dismiss()
            } catch (e: Exception) { url.error = e.message ?: getString(R.string.invalid_value) }
        }
        return scrollWrap(root)
    }
}

fun confirmRemove(context: android.content.Context, title: Int, message: String, remove: () -> Unit) {
    MaterialAlertDialogBuilder(context).setTitle(title).setMessage(message).setNegativeButton(android.R.string.cancel, null).setPositiveButton(R.string.remove) { _, _ -> remove() }.show()
}
