package com.pidal.sakamoto

import android.app.Instrumentation
import android.content.Intent
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.TunnelPackage
import com.pidal.sakamoto.ui.SourcesManagerFragment
import java.io.File
import org.json.JSONObject

/** Put package publication between a prepared row operation and its repository save. */
object SourceSaveRace {
    fun run(test: Instrumentation, undo: Boolean) {
        val ctx = test.targetContext
        val manifest = File(ctx.filesDir, "sakamoto-config.json")
        val installed = ConfigRepository.load(ctx).modeCacheID
        var published: ByteArray? = null
        var writes = 0
        val screen = SourcesManagerFragment { context, candidate, expected ->
            writes++
            if (writes == if (undo) 2 else 1) {
                if (undo) {
                    File(ctx.cacheDir, "B.zip").inputStream().use { TunnelPackage.import(ctx, it) }
                } else {
                    File(ctx.cacheDir, "threshold-open").writeText("ready")
                    val deadline = System.nanoTime() + 20_000_000_000L
                    while (ConfigRepository.load(ctx).modeCacheID == installed && System.nanoTime() < deadline) Thread.sleep(30)
                }
                check(ConfigRepository.load(ctx).modeCacheID != installed)
                published = manifest.readBytes()
            }
            ConfigRepository.save(context, candidate, expected)
        }
        val activity = test.startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        fun descendants(v: View): Sequence<View> = sequence {
            yield(v)
            if (v is ViewGroup) for (i in 0 until v.childCount) yieldAll(descendants(v.getChildAt(i)))
        }
        try {
            test.runOnMainSync {
                activity.openChild(screen, ctx.getString(R.string.profile_source_title))
                activity.supportFragmentManager.executePendingTransactions()
                var row: View? = descendants(screen.requireView()).filterIsInstance<TextView>().single { it.text == "Allowed" }
                while (row != null && !row.isLongClickable) row = row.parent as? View
                check(row?.performLongClick() == true)
            }
            if (undo) {
                test.waitForIdleSync()
                test.runOnMainSync {
                    val action = activity.findViewById<TextView>(com.google.android.material.R.id.snackbar_action)
                    check(action?.text.toString().equals(ctx.getString(R.string.undo), true))
                    check(action.performClick())
                }
            }
            check(manifest.readBytes().contentEquals(requireNotNull(published)))
            File(ctx.cacheDir, "source-save-race.json").writeText(JSONObject().put("operation", if (undo) "undo" else "delete").put("published_manifest_unchanged", true).toString())
        } finally { test.runOnMainSync { activity.finish() } }
    }
}
