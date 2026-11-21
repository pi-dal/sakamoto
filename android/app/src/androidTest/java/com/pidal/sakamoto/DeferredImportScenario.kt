package com.pidal.sakamoto

import android.app.Activity
import android.app.Instrumentation
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.net.Uri
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import com.google.android.material.R as MaterialR
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.ui.ConfigFragment
import java.io.File
import org.json.JSONObject

/** Exercise the registered Android result handlers. Only the external picker/camera result is supplied. */
object DeferredImportScenario {
    fun run(test: Instrumentation, source: String, accept: Boolean = false) {
        val ctx = test.targetContext
        val activity = test.startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        val clipboard = ctx.getSystemService(ClipboardManager::class.java)
        val conf = "[General]\n[Rule]\nDOMAIN,explicit-$source.fixture.example,PROXY\nFINAL,DIRECT\n"
        val a = ConfigRepository.load(ctx)
        fun inventory(): Map<String, List<Byte>> {
            val root = ConfigRepository.packageDirectory(ctx)
            return root.walkTopDown().filter { it.isFile }.associate { it.relativeTo(root).path to it.readBytes().toList() } +
                ("@manifest" to File(ctx.filesDir, "sakamoto-config.json").readBytes().toList())
        }
        var installed = emptyMap<String, List<Byte>>()
        fun installB() {
            if (accept) return
            File(ctx.cacheDir, "threshold-open").writeText("ready")
            val end = System.nanoTime() + 20_000_000_000L
            while (ConfigRepository.load(ctx).modeCacheID == a.modeCacheID && System.nanoTime() < end) Thread.sleep(30)
            check(ConfigRepository.load(ctx).modeCacheID != a.modeCacheID)
            installed = inventory()
        }
        fun descendants(view: View): Sequence<View> = sequence {
            yield(view)
            if (view is ViewGroup) for (i in 0 until view.childCount) yieldAll(descendants(view.getChildAt(i)))
        }
        val monitor = object : Instrumentation.ActivityMonitor() {
            override fun onStartActivity(intent: Intent): Instrumentation.ActivityResult? {
                if (source == "picker" && intent.action == Intent.ACTION_OPEN_DOCUMENT) {
                    installB()
                    return Instrumentation.ActivityResult(Activity.RESULT_OK, Intent().setData(Uri.fromFile(File(ctx.cacheDir, if (accept) "B.zip" else "A.zip"))))
                }
                if (source == "qr" && intent.action == "com.google.zxing.client.android.SCAN") {
                    installB()
                    return Instrumentation.ActivityResult(Activity.RESULT_OK, Intent().putExtra("SCAN_RESULT", conf))
                }
                return null
            }
        }
        try {
            val screen = ConfigFragment()
            test.runOnMainSync {
                if (source == "clipboard") clipboard.setPrimaryClip(ClipData.newPlainText("fixture", conf))
                activity.openChild(screen, ctx.getString(R.string.config_import_title))
                activity.supportFragmentManager.executePendingTransactions()
            }
            test.waitForIdleSync()
            test.addMonitor(monitor)
            test.runOnMainSync {
                if (source == "clipboard") {
                    val action = activity.findViewById<TextView>(MaterialR.id.snackbar_action)
                    check(action?.text.toString().equals(ctx.getString(R.string.ux_clipboard_import), true))
                    installB()
                    action.performClick()
                } else {
                    val label = if (source == "picker") R.string.tunnel_package_import else R.string.ux_import_qr
                    var row: View? = descendants(screen.requireView()).filterIsInstance<TextView>().single { it.text == ctx.getString(label) }
                    while (row != null && !row.isClickable) row = row.parent as? View
                    check(row?.performClick() == true)
                }
            }
            val end = System.nanoTime() + 5_000_000_000L
            if (accept && source == "picker") {
                while (ConfigRepository.load(ctx).modeCacheID == a.modeCacheID && System.nanoTime() < end) Thread.sleep(30)
                check(ConfigRepository.load(ctx).modeCacheID != a.modeCacheID)
                return
            }
            if (accept) {
                var saved = false
                while (!saved && System.nanoTime() < end) {
                    test.runOnMainSync {
                        val button = android.view.inspector.WindowInspector.getGlobalWindowViews().asReversed()
                            .firstNotNullOfOrNull { it.findViewById<TextView>(android.R.id.button1) }
                        saved = button?.performClick() == true
                    }
                    if (!saved) Thread.sleep(40)
                }
                test.waitForIdleSync()
                check(saved && ConfigRepository.load(ctx).sourceConfContent == conf.trim())
                return
            }
            var conflict = false
            while (!conflict && System.nanoTime() < end) {
                test.runOnMainSync { conflict = activity.findViewById<TextView>(MaterialR.id.snackbar_text)?.text == ctx.getString(R.string.editor_conflict) }
                if (!conflict) Thread.sleep(40)
            }
            check(conflict && installed.size > 1 && inventory() == installed) { "Missing conflict or changed package after $source" }
            File(ctx.cacheDir, "deferred-import.json").writeText(JSONObject().put("source", source).put("conflict_shown", true).put("verified_revision_files", installed.size - 1).toString())
        } finally {
            test.removeMonitor(monitor)
            test.runOnMainSync { if (source == "clipboard") clipboard.clearPrimaryClip(); activity.finish() }
        }
    }
}
