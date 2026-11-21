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

/** Drive the edit, then verify persisted state in a separate instrumentation process. */
object UsbEditScenario {
    fun run(test: Instrumentation, phase: String) {
        val ctx = test.targetContext
        val deletion = phase.startsWith("usb-delete")
        if (!phase.endsWith("restart")) {
            File(ctx.cacheDir, "A.zip").inputStream().use { TunnelPackage.import(ctx, it) }
            if (deletion) {
                val saved = ConfigRepository.load(ctx)
                val name = "USB fixture subscription"
                ConfigRepository.save(ctx, saved.copy(subscriptions = listOf(Triple(name, "https://allowed.fixture.example/sub", "auto"))), saved)
                val activity = test.startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
                val screen = SourcesManagerFragment()
                fun labels(view: View): Sequence<TextView> = sequence {
                    if (view is TextView) yield(view)
                    if (view is ViewGroup) for (index in 0 until view.childCount) yieldAll(labels(view.getChildAt(index)))
                }
                try {
                    test.runOnMainSync {
                        activity.openChild(screen, name)
                        activity.supportFragmentManager.executePendingTransactions()
                        var row: View? = labels(screen.requireView()).single { it.text == name }
                        while (row != null && !row.isLongClickable) row = row.parent as? View
                        check(row?.performLongClick() == true)
                    }
                } finally { test.runOnMainSync { activity.finish() } }
            } else SourceDownloadScenario.run(test, leavePending = true)
        }
        val saved = ConfigRepository.load(ctx)
        check(saved.sourceNeedsGenerate && saved.subscriptions.isEmpty())
        if (deletion) check(!saved.sourceConfIsUrl && saved.sourceConfName == "main.conf")
        else check(saved.sourceConfIsUrl && saved.sourceConfName == "ok.conf" && saved.sourceConfContent == "[Rule]\nFINAL,DIRECT\n# downloaded fixture\n")
    }
}
