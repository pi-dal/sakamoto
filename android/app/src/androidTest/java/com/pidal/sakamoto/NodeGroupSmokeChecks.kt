package com.pidal.sakamoto

import android.app.Instrumentation
import android.content.Intent
import android.os.Bundle
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import android.widget.TextView
import com.google.android.material.appbar.MaterialToolbar
import com.google.android.material.button.MaterialButton
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.ProxyGroupEditing
import com.pidal.sakamoto.ui.EditorFragment
import com.pidal.sakamoto.ui.ProxyGroupEditorFragment
import com.pidal.sakamoto.ui.SourcesManagerFragment
import java.io.File

/** Real native controls and persistence; restores the simulator's original snapshot. No VPN activation. */
class NodeGroupSmokeChecks(private val instrumentation: Instrumentation) {
    fun run() {
        val context = instrumentation.targetContext
        val original = ConfigRepository.load(context)
        val links = listOf("trojan://test@a.example:443#Review%20A", "trojan://test@b.example:443#Review%20B")
        val config = """{"log":{"level":"warn"},"dns":{"servers":[{"type":"udp","tag":"dns-direct","server":"1.1.1.1"}]},"inbounds":[{"type":"tun","tag":"tun-in","address":["172.19.0.1/30"],"auto_route":true,"stack":"mixed"}],"outbounds":[{"type":"selector","tag":"MainProxy","outbounds":["Review A","Review B"],"default":"Review A"},{"type":"trojan","tag":"Review A","server":"a.example","server_port":443,"password":"test","tls":{"enabled":true}},{"type":"trojan","tag":"Review B","server":"b.example","server_port":443,"password":"test","tls":{"enabled":true}},{"type":"direct","tag":"direct"}],"route":{"final":"MainProxy","auto_detect_interface":true}}"""
        fun descendants(view: View): List<View> = listOf(view) + if (view is ViewGroup) (0 until view.childCount).flatMap { descendants(view.getChildAt(it)) } else emptyList()
        fun settle() { instrumentation.waitForIdleSync(); Thread.sleep(250) }
        fun capture(name: String) {
            settle()
            val bitmap = checkNotNull(instrumentation.uiAutomation.takeScreenshot())
            File(context.cacheDir, name).outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }; bitmap.recycle()
        }
        try {
            ConfigRepository.save(context, ConfigRepository.StagedConfig(generatedContent = config, nodes = links))
            val activity = instrumentation.startActivitySync(Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
            lateinit var sources: SourcesManagerFragment
            instrumentation.runOnMainSync {
                sources = SourcesManagerFragment(); activity.openChild(sources, context.getString(R.string.profile_source_title)); activity.supportFragmentManager.executePendingTransactions()
            }
            settle()
            lateinit var editor: EditorFragment
            instrumentation.runOnMainSync {
                var target: View = descendants(sources.requireView()).filterIsInstance<TextView>().first { it.text.toString() == "Review A" }
                while (!target.isClickable && target.parent is View) target = target.parent as View
                check(target.performClick())
                activity.supportFragmentManager.executePendingTransactions()
                editor = activity.supportFragmentManager.primaryNavigationFragment as EditorFragment
                val fields = descendants(editor.requireView()).filterIsInstance<EditText>()
                fields.first { (it.parent.parent as com.google.android.material.textfield.TextInputLayout).hint == context.getString(R.string.node_remark) }.setText("Renamed node")
                fields.first { (it.parent.parent as com.google.android.material.textfield.TextInputLayout).hint == context.getString(R.string.node_address) }.setText("192.0.2.8")
            }
            capture("nodes-edit.png")
            instrumentation.runOnMainSync {
                activity.findViewById<MaterialToolbar>(R.id.top_app_bar).menu.performIdentifierAction(R.id.editor_save, 0)
                activity.supportFragmentManager.executePendingTransactions()
            }
            settle()
            val edited = ConfigRepository.load(context)
            check(edited.nodeAliases["Review A"] == "Renamed node")
            check(ProxyGroupEditing.rows(edited.generatedContent).first { it.optString("tag") == "Renamed node" }.getString("server") == "192.0.2.8")
            check(ProxyGroupEditing.groups(edited.generatedContent).single().defaultMember == "Renamed node")
            instrumentation.runOnMainSync {
                descendants(sources.requireView()).first { it.contentDescription == context.getString(R.string.remove) }.performClick()
            }
            settle()
            check(ConfigRepository.load(context).nodes.size == 1)
            instrumentation.runOnMainSync { check(descendants(sources.requireView()).filterIsInstance<TextView>().none { it.text.toString() == "Review A" }) }
            capture("nodes-deleted-undo.png")
            instrumentation.runOnMainSync {
                descendants(activity.window.decorView).filterIsInstance<TextView>().first { it.text == context.getString(R.string.undo) }.performClick()
            }
            settle()
            check(ConfigRepository.load(context).nodes.size == 2)
            lateinit var groups: ProxyGroupEditorFragment
            instrumentation.runOnMainSync {
                groups = ProxyGroupEditorFragment().apply { arguments = Bundle().apply { putString("tag", "MainProxy") } }
                activity.openChild(groups, context.getString(R.string.group_edit)); activity.supportFragmentManager.executePendingTransactions()
            }
            settle(); capture("groups-edit.png")
            instrumentation.runOnMainSync {
                val controls = descendants(groups.requireView())
                controls.first { it.contentDescription == context.getString(R.string.group_move_down, "Renamed node") }.apply {
                    check(width >= 48 * context.resources.displayMetrics.density); check(height >= 48 * context.resources.displayMetrics.density); performClick()
                }
            }
            instrumentation.runOnMainSync {
                descendants(groups.requireView()).first { it.contentDescription == context.getString(R.string.group_remove_member, "Renamed node") }.performClick()
            }
            instrumentation.runOnMainSync { activity.findViewById<MaterialToolbar>(R.id.top_app_bar).menu.performIdentifierAction(R.id.editor_save, 0) }
            settle()
            check(ProxyGroupEditing.groups(ConfigRepository.load(context).generatedContent).first { it.tag == "MainProxy" }.members == listOf("Review B"))
            instrumentation.runOnMainSync {
                activity.openChild(ProxyGroupEditorFragment().apply { arguments = Bundle().apply { putString("tag", "MainProxy") } }, context.getString(R.string.group_edit))
                activity.supportFragmentManager.executePendingTransactions()
            }
            settle(); capture("groups-saved.png")
            var automaticConfig = ProxyGroupEditing.apply(ConfigRepository.load(context).generatedContent,
                com.pidal.sakamoto.runtime.ProxyGroupEdit("RealityAuto", "urltest", listOf("Renamed node", "Review B")))
            automaticConfig = ProxyGroupEditing.apply(automaticConfig,
                com.pidal.sakamoto.runtime.ProxyGroupEdit("ManualPick", members = listOf("Renamed node", "Review B")))
            ConfigRepository.saveGeneratedEdit(context, automaticConfig)
            instrumentation.runOnMainSync {
                groups = ProxyGroupEditorFragment().apply { arguments = Bundle().apply { putString("tag", "RealityAuto") } }
                activity.openChild(groups, context.getString(R.string.group_edit)); activity.supportFragmentManager.executePendingTransactions()
            }
            settle()
            instrumentation.runOnMainSync {
                check(descendants(groups.requireView()).filterIsInstance<TextView>().any { it.text.toString() == context.getString(R.string.group_automatic_members_note) })
                check(descendants(groups.requireView()).first { it.contentDescription == context.getString(R.string.group_remove_member, "Review B") }.performClick())
                activity.findViewById<MaterialToolbar>(R.id.top_app_bar).menu.performIdentifierAction(R.id.editor_save, 0)
            }
            settle()
            val automaticSaved = ConfigRepository.load(context).generatedContent
            check(ProxyGroupEditing.groups(automaticSaved).first { it.tag == "RealityAuto" }.members == listOf("Renamed node"))
            check(ProxyGroupEditing.groups(automaticSaved).first { it.tag == "ManualPick" }.members == listOf("Renamed node", "Review B"))
            check(ProxyGroupEditing.rows(automaticSaved).any { it.optString("tag") == "Review B" })
            instrumentation.runOnMainSync {
                groups = ProxyGroupEditorFragment().apply { arguments = Bundle().apply { putString("tag", "RealityAuto") } }
                activity.openChild(groups, context.getString(R.string.group_edit)); activity.supportFragmentManager.executePendingTransactions()
            }
            settle(); capture("reality-auto-excluded-member.png")
            lateinit var policy: EditorFragment
            instrumentation.runOnMainSync {
                policy = EditorFragment().apply { arguments = Bundle().apply { putString("kind", "rule"); putInt("index", -1) } }
                activity.openChild(policy, context.getString(R.string.add_policy_action))
                activity.supportFragmentManager.executePendingTransactions()
                var matchType: View = descendants(policy.requireView()).filterIsInstance<TextView>().first { it.text.toString() == context.getString(R.string.rule_match_type) }
                while (!matchType.isClickable && matchType.parent is View) matchType = matchType.parent as View
                check(matchType.performClick())
            }
            settle()
            instrumentation.uiAutomation.let { automation ->
                val root = checkNotNull(automation.rootInActiveWindow)
                val option = root.findAccessibilityNodeInfosByText(context.getString(R.string.ux_editing_pred_domain_suffix)).first()
                check(option.performAction(android.view.accessibility.AccessibilityNodeInfo.ACTION_CLICK))
            }
            settle()
            instrumentation.runOnMainSync {
                descendants(policy.requireView()).filterIsInstance<EditText>().single().setText("*.aihubmix.com")
            }
            capture("policy-domain-and-subdomains.png")
            instrumentation.runOnMainSync { activity.findViewById<MaterialToolbar>(R.id.top_app_bar).menu.performIdentifierAction(R.id.editor_save, 0) }
            settle()
            val rule = org.json.JSONObject(ConfigRepository.load(context).generatedContent).getJSONObject("route").getJSONArray("rules").getJSONObject(0)
            check(rule.getJSONArray("domain_suffix").getString(0) == "aihubmix.com")
            check(!rule.has("domain"))
            instrumentation.runOnMainSync {
                policy = EditorFragment().apply { arguments = Bundle().apply { putString("kind", "rule"); putInt("index", 0) } }
                activity.openChild(policy, context.getString(R.string.edit_rule)); activity.supportFragmentManager.executePendingTransactions()
            }
            settle()
            instrumentation.runOnMainSync {
                check(descendants(policy.requireView()).filterIsInstance<EditText>().single().text.toString() == "aihubmix.com")
                check(descendants(policy.requireView()).filterIsInstance<TextView>().any { it.text.toString() == context.getString(R.string.ux_editing_pred_domain_suffix) })
                activity.finish()
            }
        } finally { ConfigRepository.save(context, original) }
    }
}
