package com.pidal.sakamoto

import android.app.Instrumentation
import android.content.Intent
import android.view.ViewGroup
import android.widget.TextView
import androidx.core.view.descendants
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.job
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.S3SyncRepository
import com.pidal.sakamoto.runtime.TunnelPackage
import com.pidal.sakamoto.ui.S3SettingsFragment
import org.json.JSONObject
import java.io.File

object S3ProfileScenario {
    fun run(test: Instrumentation, phase: String) {
        val ctx = test.targetContext
        val repo = S3SyncRepository(ctx)
        fun sync(prefix: String, fault: String = "", configure: Boolean = true) {
            val release = File(ctx.cacheDir, "s3-release")
            val mode = android.system.Os.stat(ctx.filesDir.path).st_mode and 511
            val settings = JSONObject().put("enabled", true).put("region", "auto").put("bucket", "fixture")
                .put("endpoint", "https://127.0.0.1:18585").put("prefix", prefix)
            if (configure) repo.save(settings, JSONObject().put("access_key", "fixture").put("secret_key", "fixture"))
            val activity = test.startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
            val screen = S3SettingsFragment()
            try {
                test.runOnMainSync {
                    activity.openChild(screen, "S3 Sync")
                    activity.supportFragmentManager.executePendingTransactions()
                    val views = (screen.requireView() as ViewGroup).descendants
                    if (fault == "save" || fault == "read") {
                        views.filterIsInstance<android.widget.EditText>().single { it.text.toString() == prefix }.setText("rejected-target")
                        if (fault == "save") android.system.Os.chmod(ctx.filesDir.path, 365)
                        else android.system.Os.chmod(ctx.getFileStreamPath("s3-credentials.bin").path, 0)
                        views.filterIsInstance<TextView>().single { it.text == ctx.getString(R.string.s3_save) }.performClick()
                        check(views.filterIsInstance<TextView>().any { it.text.startsWith("Error:") })
                    } else {
                        views.filterIsInstance<TextView>().single { it.text == ctx.getString(R.string.s3_sync_now) }.performClick()
                    }
                }
                if (fault == "save" || fault == "read") return
                if (fault.isNotEmpty() && fault != "recovery") {
                    val waiting = File(ctx.cacheDir, "s3-waiting")
                    repeat(400) { if (!waiting.exists()) Thread.sleep(50) }
                    check(waiting.exists())
                    if (fault == "cancel") {
                        lateinit var job: kotlinx.coroutines.Job
                        test.runOnMainSync {
                            job = screen.viewLifecycleOwner.lifecycleScope.coroutineContext.job
                            activity.onBackPressedDispatcher.onBackPressed()
                            activity.supportFragmentManager.executePendingTransactions()
                        }
                        test.waitForIdleSync()
                        repeat(100) { if (!job.isCancelled) Thread.sleep(20) }
                        check(job.isCancelled)
                        release.createNewFile()
                        runBlocking { withTimeout(20000) { job.join() } }
                        return
                    }
                    if (fault == "disk") android.system.Os.chmod(ctx.filesDir.path, 365)
                    release.createNewFile()
                }
                repeat(400) {
                    var detail = ""
                    test.runOnMainSync {
                        detail = (screen.requireView() as ViewGroup).descendants.filterIsInstance<TextView>()
                            .map { it.text.toString() }.firstOrNull { it.startsWith("Synced ") || it.startsWith("Error:") }.orEmpty()
                    }
                    if (detail.isNotEmpty() && fault == "recovery") {
                        check(detail.startsWith("Error:") && "nodes-before-sync.txt" in detail) { detail }; return
                    }
                    if (detail.isNotEmpty()) { check(detail.startsWith(if (fault == "disk" || fault == "switch") "Error:" else "Synced ")) { detail }; return }
                    Thread.sleep(100)
                }
                error("S3 settings page did not finish syncing")
            } finally {
                if (fault == "read") android.system.Os.chmod(ctx.getFileStreamPath("s3-credentials.bin").path, 384)
                if (fault == "disk" || fault == "save") android.system.Os.chmod(ctx.filesDir.path, mode)
                test.runOnMainSync { activity.finish() }
            }
        }
        if (phase in setOf("s3-download-qr", "s3-download-clipboard")) {
            val source = phase.removePrefix("s3-download-")
            File(ctx.cacheDir, "B.zip").inputStream().use { TunnelPackage.import(ctx, it) }
            DeferredImportScenario.run(test, source, accept = true)
            sync(source)
            File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("source", source).toString())
            return
        }
        if (phase == "s3-download-duplicates-switch" || phase == "s3-download-duplicates-switched") {
            if (phase.endsWith("-switch")) DeferredImportScenario.run(test, "picker", accept = true)
            else SourceRevisionScenario.run(test, exportRecovery = true)
            sync("new-profile", configure = phase.endsWith("-switch"))
            File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("revision", ConfigRepository.load(ctx).packageRevision).toString())
            return
        }
        if (phase.startsWith("s3-download-node-text") || phase.startsWith("s3-download-duplicates")) {
            val prefix = if (phase.startsWith("s3-download-duplicates")) "duplicates" else "node-text"
            val initial = phase == "s3-download-$prefix"
            if (initial) File(ctx.cacheDir, "nested.zip").inputStream().use { TunnelPackage.import(ctx, it) }
            if (phase.endsWith("-local-delete") || phase.endsWith("-first") || phase.endsWith("-second") || phase.endsWith("-conflict")) {
                val activity = test.startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
                try {
                    val screen = com.pidal.sakamoto.ui.SourcesManagerFragment()
                    test.runOnMainSync {
                        activity.openChild(screen, "Sources")
                        activity.supportFragmentManager.executePendingTransactions()
                        val remove = (screen.requireView() as ViewGroup).descendants.filter { it.contentDescription == ctx.getString(R.string.remove) }.toList()
                        val row = if (phase.endsWith("-first")) 0 else 1
                        check(remove.size >= 2 && remove[row].performClick())
                    }
                    check(ConfigRepository.load(ctx).nodes.single().endsWith("#remote"))
                } finally { test.runOnMainSync { activity.finish() } }
            }
            if (phase.endsWith("-retry")) SourceRevisionScenario.run(test, archiveRecovery = true)
            if (phase.endsWith("-conflict")) {
                val manifest = File(ctx.filesDir, "sakamoto-config.json")
                val before = manifest.readBytes()
                sync(prefix, fault = "recovery", configure = false)
                check(before.contentEquals(manifest.readBytes())) { "Recovery conflict changed manifest" }
            } else sync(prefix, configure = initial)
            File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("nodes", ConfigRepository.load(ctx).nodes.size).toString())
            return
        }
        if (phase == "s3-download-nested-edit") {
            File(ctx.cacheDir, "nested.zip").inputStream().use { TunnelPackage.import(ctx, it) }
            sync("nested-edit")
            sync("nested-edit", configure = false)
            check(ConfigRepository.load(ctx).sourceConfContent.contains("remote-main-update"))
            File(ctx.cacheDir, "$phase.json").writeText("{}")
            return
        }
        if (phase == "s3-download-nested-edit-restart") {
            SourceRevisionScenario.run(test, nestedEdit = true)
            SourceRevisionScenario.run(test, appendNode = true)
            sync("nested-edit", configure = false)
            val staged = ConfigRepository.load(ctx)
            check(File(ConfigRepository.packageDirectory(ctx), "imports/local/main.conf").readText() == staged.sourceConfContent)
            File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("main", staged.sourceConfPath).toString())
            return
        }
        if (phase == "s3-download-nested") {
            File(ctx.cacheDir, "nested.zip").inputStream().use { TunnelPackage.import(ctx, it) }
            sync("nested")
            File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("initial_sync", true).toString())
            return
        }
        if (phase.startsWith("s3-credentials-")) {
            if (phase.endsWith("fault")) {
                File(ctx.cacheDir, "B.zip").inputStream().use { TunnelPackage.import(ctx, it) }
                sync("credentials")
                val vault = File(ctx.filesDir, "s3-credentials.bin")
                val original = vault.readBytes()
                sync("credentials", "read", configure = false)
                check(original.contentEquals(vault.readBytes()))
                sync("credentials", "save", configure = false)
                check(original.contentEquals(vault.readBytes()))
            } else {
                check(repo.settings().getString("prefix") == "credentials")
                sync("credentials", configure = false)
            }
            File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("original_settings", repo.settings().getString("prefix") == "credentials").toString())
            return
        }
        if (phase.startsWith("s3-download-upload-")) {
            val prefix = phase.removePrefix("s3-download-")
            File(ctx.cacheDir, "A.zip").inputStream().use { TunnelPackage.import(ctx, it) }
            sync(prefix)
            val before = ConfigRepository.load(ctx)
            ConfigRepository.save(ctx, before.copy(sourceConfContent = before.sourceConfContent + "\n# pending-upload-marker\n"), before)
            for (name in listOf("s3-waiting", "s3-release")) File(ctx.cacheDir, name).delete()
            sync(prefix, prefix.removePrefix("upload-"))
            File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("local_marker", "pending-upload-marker" in ConfigRepository.load(ctx).sourceConfContent).toString())
            return
        }
        if (phase.startsWith("s3-download-")) {
            val fault = phase.removePrefix("s3-download-")
            File(ctx.cacheDir, "B.zip").inputStream().use { TunnelPackage.import(ctx, it) }
            sync(fault)
            val receipt = ConfigRepository.manifestReceipt(ctx)
            File(ctx.cacheDir, "s3-original").writeText(receipt)
            for (name in listOf("s3-waiting", "s3-release")) File(ctx.cacheDir, name).delete()
            sync(fault, fault)
            val after = ConfigRepository.load(ctx)
            val same = receipt == ConfigRepository.manifestReceipt(ctx)
            check(same == (fault != "control"))
            check(after.sourceNeedsGenerate == !same)
            check(after.policy.any { it.match == "explicit-direct.fixture.example" } == !same)
            File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("same_manifest", same)
                .put("pending", after.sourceNeedsGenerate).put("policy_count", after.policy.size).toString())
            return
        }
        if (phase == "s3-switch") {
            fun install(name: String) = File(ctx.cacheDir, name).inputStream().use { TunnelPackage.import(ctx, it) }
            install("A.zip")
            val a = ConfigRepository.load(ctx)
            val main = ConfigRepository.sourceFiles(ctx).single { it.name == a.sourceConfName }
            ConfigRepository.editSource(ctx, a, main, a.sourceConfContent + "\n# private-source-A-fixture\n")
            sync("A")
            install("B.zip")
            SourceDownloadScenario.run(test, leavePending = true)
            check(ConfigRepository.load(ctx).sourceConfIsUrl)
            sync("B")
        }
        val b = ConfigRepository.load(ctx)
        File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("url_source", b.sourceConfIsUrl)
            .put("downloaded_B", b.sourceConfName == "ok.conf" && "downloaded fixture" in b.sourceConfContent)
            .put("A_marker", "private-source-A-fixture" in b.sourceConfContent).toString())
    }
}
