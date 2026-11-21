package com.pidal.sakamoto

import android.app.Instrumentation
import android.content.Intent
import android.view.View
import android.view.ViewGroup
import android.view.inspector.WindowInspector
import android.widget.EditText
import android.widget.TextView
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.ui.SourceFilesFragment

/** Exercise two saves on the same fragment and its actual Material dialogs. */
object SourceRevisionScenario {
    fun run(test: Instrumentation, changeRoute: Boolean = false, existing: MainActivity? = null, nestedEdit: Boolean = false, appendNode: Boolean = false, archiveRecovery: Boolean = false, exportRecovery: Boolean = false) {
        val ctx = test.targetContext
        val activity = existing ?: test.startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        val screen = SourceFilesFragment()
        fun descendants(view: View): Sequence<View> = sequence {
            yield(view)
            if (view is ViewGroup) for (i in 0 until view.childCount) yieldAll(descendants(view.getChildAt(i)))
        }
        fun interact(label: String, action: (Sequence<View>) -> Boolean) {
            val end = System.nanoTime() + 5_000_000_000L
            do {
                var handled = false
                test.runOnMainSync { handled = action(WindowInspector.getGlobalWindowViews().asReversed().asSequence().flatMap(::descendants).filter { it.isShown }) }
                if (handled) return
                Thread.sleep(40)
            } while (System.nanoTime() < end)
            error("Source editor did not show $label")
        }
        fun click(text: String) = interact(text) { views ->
            val matches = views.filterIsInstance<TextView>().filter { it.text.toString().equals(text, true) }
            var row: View? = if (text.endsWith(".conf")) matches.lastOrNull() else matches.firstOrNull()
            while (row != null && !row.isClickable) row = row.parent as? View
            row?.performClick() == true
        }
        try {
            test.runOnMainSync { activity.openChild(screen, ctx.getString(R.string.source_files)); activity.supportFragmentManager.executePendingTransactions() }
            val initial = ConfigRepository.load(ctx)
            if (exportRecovery) {
                // The caller supplies the exact two labels from the outgoing revision.
                val labels = java.io.File(ctx.cacheDir, "recovery-labels").readLines()
                labels.forEachIndexed { index, label ->
                    val output = java.io.File(ctx.cacheDir, "exported-recovery-$index.txt")
                    output.delete()
                    val resolver = ctx.contentResolver
                    val uri = checkNotNull(resolver.insert(android.provider.MediaStore.Downloads.EXTERNAL_CONTENT_URI, android.content.ContentValues().apply {
                        put(android.provider.MediaStore.MediaColumns.DISPLAY_NAME, "sakamoto-test-${java.util.UUID.randomUUID()}.txt")
                        put(android.provider.MediaStore.MediaColumns.MIME_TYPE, "text/plain")
                        put(android.provider.MediaStore.MediaColumns.IS_PENDING, 1)
                    }))
                    checkNotNull(resolver.openOutputStream(uri)).close()
                    val monitor = object : Instrumentation.ActivityMonitor() {
                        override fun onStartActivity(intent: Intent): Instrumentation.ActivityResult? =
                            if (intent.action == Intent.ACTION_CREATE_DOCUMENT) Instrumentation.ActivityResult(android.app.Activity.RESULT_OK,
                                Intent().setData(uri)) else null
                    }
                    test.addMonitor(monitor)
                    try {
                        click(label)
                        click("Save a copy")
                        val end = System.nanoTime() + 5_000_000_000L
                        val expectedLength = label.substringAfterLast(" · ").let { hash ->
                            val file = com.pidal.sakamoto.runtime.RecoveryFiles.saved(ctx).single { it.name == label.substringBeforeLast(" · ") && it.parentFile!!.name.startsWith(hash) }
                            file.length()
                        }
                        while (System.nanoTime() < end) {
                            resolver.openInputStream(uri)!!.use { input -> output.outputStream().use { input.copyTo(it) } }
                            if (output.length() == expectedLength) break
                            Thread.sleep(30)
                        }
                        check(output.length() == expectedLength)
                    } finally { test.removeMonitor(monitor); resolver.delete(uri, null, null) }
                }
                return
            }
            if (archiveRecovery) {
                click("nodes-before-sync.txt")
                click("Archive copy")
                interact("archived recovery file") { views -> views.filterIsInstance<TextView>().any { it.text.toString().startsWith("nodes-before-sync-archived-") && it.text.toString().endsWith(".txt") } }
                check(ConfigRepository.load(ctx).packageRevision != initial.packageRevision)
                return
            }
            val name = when { appendNode -> "nodes.txt"; nestedEdit -> "nested/base.conf"; else -> initial.sourceConfPath }
            for (pass in 1..2) {
                click(name)
                click(ctx.getString(R.string.edit))
                interact("edit field") { views ->
                    val field = views.filterIsInstance<EditText>().firstOrNull() ?: return@interact false
                    if (appendNode) {
                        check(field.text.toString().trim() == "hy2://fixture@192.0.2.31:443#remote") { "Node editor opened stale source" }
                        field.append("\nhy2://fixture@192.0.2.32:443#added\n")
                        return@interact true
                    }
                    if (nestedEdit) {
                        val remoteRule = "explicit-include.fixture.example,DIRECT"
                        check(remoteRule in field.text.toString())
                        field.setText(field.text.toString().replace(remoteRule, "explicit-include.fixture.example,PROXY"))
                    }
                    if (changeRoute && pass == 1) field.setText(field.text.toString().replace("explicit-direct.fixture.example,DIRECT", "explicit-direct.fixture.example,PROXY"))
                    if (!nestedEdit) field.append("\n# editor-pass-$pass\n")
                    true
                }
                click(ctx.getString(R.string.save))
                val updated = ConfigRepository.load(ctx)
                if (nestedEdit || appendNode) {
                    check(updated.packageRevision != initial.packageRevision)
                    return
                }
                check(updated.sourceConfContent.contains("editor-pass-$pass"))
                click(name)
                click(ctx.getString(R.string.source_open_preview))
                interact("preview $pass") { views -> views.filterIsInstance<TextView>().any { it.text.contains("editor-pass-$pass") } }
                click(ctx.getString(android.R.string.ok))
            }
        } finally { if (existing == null) test.runOnMainSync { activity.finish() } }
    }
}
