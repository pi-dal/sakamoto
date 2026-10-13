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
import com.pidal.sakamoto.ui.AddNodeSheet
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
            lateinit var sheet: AddNodeSheet
            instrumentation.runOnMainSync {
                var target: View = descendants(sources.requireView()).filterIsInstance<TextView>().first { it.text.toString() == "Review A" }
                while (!target.isClickable && target.parent is View) target = target.parent as View
                check(target.performClick())
                activity.supportFragmentManager.executePendingTransactions()
                sheet = activity.supportFragmentManager.findFragmentByTag("edit-node") as AddNodeSheet
                val editor = descendants(sheet.requireDialog().window!!.decorView).filterIsInstance<EditText>().first()
                editor.setText("trojan://test@edited.example:8443#Review%20A")
            }
            capture("nodes-edit.png")
            instrumentation.runOnMainSync {
                descendants(sheet.requireDialog().window!!.decorView).filterIsInstance<MaterialButton>().first { it.text == context.getString(R.string.save) }.performClick()
                activity.supportFragmentManager.executePendingTransactions()
            }
            settle()
            check(ConfigRepository.load(context).nodes.first().contains("edited.example:8443"))
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
                controls.first { it.contentDescription == context.getString(R.string.group_move_down, "Review A") }.apply {
                    check(width >= 48 * context.resources.displayMetrics.density); check(height >= 48 * context.resources.displayMetrics.density); performClick()
                }
            }
            instrumentation.runOnMainSync {
                descendants(groups.requireView()).first { it.contentDescription == context.getString(R.string.group_remove_member, "Review A") }.performClick()
            }
            instrumentation.runOnMainSync { activity.findViewById<MaterialToolbar>(R.id.top_app_bar).menu.performIdentifierAction(R.id.editor_save, 0) }
            settle()
            check(ProxyGroupEditing.groups(ConfigRepository.load(context).generatedContent).first { it.tag == "MainProxy" }.members == listOf("Review B"))
            instrumentation.runOnMainSync {
                activity.openChild(ProxyGroupEditorFragment().apply { arguments = Bundle().apply { putString("tag", "MainProxy") } }, context.getString(R.string.group_edit))
                activity.supportFragmentManager.executePendingTransactions()
            }
            settle(); capture("groups-saved.png")
            instrumentation.runOnMainSync { activity.finish() }
        } finally { ConfigRepository.save(context, original) }
    }
}
