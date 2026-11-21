package com.pidal.sakamoto

import android.app.Instrumentation
import android.content.Intent
import android.view.View
import android.view.ViewGroup
import android.view.inspector.WindowInspector
import android.widget.EditText
import android.widget.TextView
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.ExperimentRuntime
import com.pidal.sakamoto.ui.ConfigFragment
import com.pidal.sakamoto.ui.SourcesManagerFragment
import com.pidal.sakamoto.ui.ExperimentsFragment
import java.io.File

object StaleThresholdScenario {
    fun run(test: Instrumentation, editor: String) {
        val ctx = test.targetContext
        val a = ConfigRepository.load(ctx)
        check(ExperimentRuntime.settings(ctx).mode == "auto")
        val activity = test.startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        fun descendants(view: View): Sequence<View> = sequence {
            yield(view)
            if (view is ViewGroup) for (i in 0 until view.childCount) yieldAll(descendants(view.getChildAt(i)))
        }
        try {
            val resolverCalls = java.util.concurrent.atomic.AtomicInteger()
            val screen = when (editor) {
                "url" -> ConfigFragment(okhttp3.OkHttpClient.Builder().proxy(java.net.Proxy.NO_PROXY).dns(object : okhttp3.Dns {
                    override fun lookup(hostname: String): List<java.net.InetAddress> {
                        resolverCalls.incrementAndGet()
                        return listOf(java.net.InetAddress.getLoopbackAddress())
                    }
                }).build())
                "json" -> ConfigFragment()
                "node", "subscription" -> SourcesManagerFragment()
                else -> ExperimentsFragment()
            }
            val label = when (editor) {
                "url" -> R.string.config_import_title
                "json" -> R.string.edit_generated_config
                "node" -> R.string.add_node_action
                "subscription" -> R.string.add_subscription_action
                else -> R.string.experiment_threshold
            }
            test.runOnMainSync {
                activity.openChild(screen, ctx.getString(R.string.experiments_title))
                activity.supportFragmentManager.executePendingTransactions()
                var row: View? = descendants(screen.requireView()).filterIsInstance<TextView>().single { it.text == ctx.getString(label) }
                while (row != null && !row.isClickable) row = row.parent as? View
                check(row?.performClick() == true)
            }
            test.waitForIdleSync()
            val dialog = WindowInspector.getGlobalWindowViews().single { window -> descendants(window).any { it is EditText } }
            test.runOnMainSync {
                val inputs = descendants(dialog).filterIsInstance<EditText>().toList()
                when (editor) {
                    "url" -> inputs.first().setText("https://allowed.fixture.example:18585/stale.conf")
                    "node" -> inputs.first().setText("hy2://fixture@192.0.2.99:443#stale-node")
                    "subscription" -> { inputs[0].setText("stale subscription"); inputs[1].setText("https://stale.fixture.example/sub") }
                    "json" -> inputs.first().append(" ")
                    else -> inputs.first().setText("7")
                }
            }
            File(ctx.cacheDir, "threshold-open").writeText("ready")
            val until = System.nanoTime() + 25_000_000_000L
            while (ConfigRepository.load(ctx).modeCacheID == a.modeCacheID && System.nanoTime() < until) Thread.sleep(50)
            check(ConfigRepository.load(ctx).modeCacheID != a.modeCacheID && ExperimentRuntime.settings(ctx).mode == "off")
            val manifest = File(ctx.filesDir, "sakamoto-config.json")
            val b = manifest.readBytes()
            test.runOnMainSync {
                val submit = dialog.findViewById<View>(android.R.id.button1) ?: descendants(dialog).filterIsInstance<TextView>().single { it.text.toString().equals(ctx.getString(R.string.add), true) }
                check(submit.performClick())
            }
            if (editor == "url") {
                test.runOnMainSync { check(!(screen as ConfigFragment).importing) }
                check(resolverCalls.get() == 0)
                File(ctx.cacheDir, "stale-url.json").writeText("{\"resolver_calls\":0,\"import_started\":false}")
            }
            check(manifest.readBytes().contentEquals(b) && ExperimentRuntime.settings(ctx).mode == "off") { "Stale $editor editor overwrote B" }
        } finally { test.runOnMainSync { activity.finish() } }
    }
}
