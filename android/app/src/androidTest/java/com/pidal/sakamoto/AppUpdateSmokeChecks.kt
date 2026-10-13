package com.pidal.sakamoto

import android.app.Instrumentation
import android.content.Intent
import android.content.pm.PackageManager
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.lifecycle.ViewModelProvider
import com.google.android.material.button.MaterialButton
import com.pidal.sakamoto.runtime.AndroidUpdate
import com.pidal.sakamoto.runtime.AndroidUpdateRelease
import com.pidal.sakamoto.runtime.AppUpdateRepository
import com.pidal.sakamoto.ui.AppUpdateFragment
import com.pidal.sakamoto.ui.AppUpdateModel
import com.pidal.sakamoto.ui.UpdatePhase
import com.pidal.sakamoto.ui.UpdateScreenState
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.asResponseBody
import okhttp3.MediaType.Companion.toMediaType
import okio.buffer
import okio.source
import org.json.JSONObject
import java.io.File
import java.security.MessageDigest

/** Synthetic installed-version facts exercise the real published APK, signature parser and private cache. */
class AppUpdateSmokeChecks(private val instrumentation: Instrumentation) {
    @Suppress("DEPRECATION", "UNCHECKED_CAST")
    fun run() {
        val context = instrumentation.targetContext
        val fixture = File(context.cacheDir, "update-test/official.apk")
        check(fixture.isFile) { "Stage an official signed release in cache/update-test/official.apk" }
        val flags = if (android.os.Build.VERSION.SDK_INT >= 28) PackageManager.GET_SIGNING_CERTIFICATES else PackageManager.GET_SIGNATURES
        val archive = checkNotNull(context.packageManager.getPackageArchiveInfo(fixture.path, flags))
        val version = archive.versionName!!
        val code = archive.versionCode.toLong()
        val signatures = checkNotNull(if (android.os.Build.VERSION.SDK_INT >= 28) archive.signingInfo!!.apkContentsSigners else archive.signatures)
        fun hash(file: File): String = file.inputStream().use { input ->
            val digest = MessageDigest.getInstance("SHA-256"); val buffer = ByteArray(65536)
            while (true) { val count = input.read(buffer); if (count < 0) break; digest.update(buffer, 0, count) }
            digest.digest().joinToString("") { "%02x".format(it) }
        }
        val signer = MessageDigest.getInstance("SHA-256").digest(signatures[0].toByteArray()).joinToString("") { "%02x".format(it) }
        val sha = hash(fixture)
        val file = "sakamoto-android-v$version.apk"
        val tag = "android-v$version"
        val asset = JSONObject().put("browser_download_url", "${AndroidUpdateRelease.REPOSITORY}/releases/download/$tag/$file").put("size", fixture.length())
        val update = AndroidUpdate(AndroidUpdateRelease(tag, "Synthetic update test fixture", mapOf(file to asset)), code, file, "universal", fixture.length(), sha, signer)
        archive.versionCode -= 1
        val client = OkHttpClient.Builder().addInterceptor { chain ->
            Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1).code(200).message("OK")
                .body(fixture.source().buffer().asResponseBody("application/vnd.android.package-archive".toMediaType(), fixture.length())).build()
        }.build()
        val cache = File(context.cacheDir, "update-test/verified")
        cache.deleteRecursively()
        val repository = AppUpdateRepository(context, client, { archive }, cache)
        runBlocking { withContext(Dispatchers.IO) {
            repository.download(update) {}
            check(repository.restore()?.sha256 == sha)
            check(repository.verifiedInstaller(update).isFile)
            // Debug and other signers cannot install the officially signed candidate.
            val differentSigner = context.packageManager.getPackageInfo(context.packageName, flags).apply { versionCode -= 1 }
            val real = AppUpdateRepository(context, client, { differentSigner }, cache)
            check(runCatching { real.verifiedInstaller(update) }.exceptionOrNull()?.message?.contains("signature") == true)
            val saved = File(cache, "update.apk"); check(saved.setWritable(true)); saved.appendBytes(byteArrayOf(0))
            check(runCatching { repository.verifiedInstaller(update) }.isFailure)
            check(repository.restore() == null)
            check(!saved.exists())
            val corrupt = update.copy(sha256 = "0".repeat(64))
            check(runCatching { repository.download(corrupt) {} }.isFailure)
            check(cache.listFiles().orEmpty().none { it.extension == "part" || it.extension == "apk" })
        } }
        val activity = instrumentation.startActivitySync(Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        lateinit var fragment: AppUpdateFragment
        instrumentation.runOnMainSync {
            fragment = AppUpdateFragment(); activity.openChild(fragment, context.getString(R.string.app_updates)); activity.supportFragmentManager.executePendingTransactions()
        }
        instrumentation.waitForIdleSync(); Thread.sleep(300)
        fun descendants(view: View): List<View> = listOf(view) + if (view is ViewGroup) (0 until view.childCount).flatMap { descendants(view.getChildAt(it)) } else emptyList()
        fun capture(name: String) {
            instrumentation.waitForIdleSync(); Thread.sleep(150)
            val bitmap = checkNotNull(instrumentation.uiAutomation.takeScreenshot())
            File(context.cacheDir, name).outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }; bitmap.recycle()
        }
        lateinit var state: MutableStateFlow<UpdateScreenState>
        instrumentation.runOnMainSync {
            val model = ViewModelProvider(fragment)[AppUpdateModel::class.java]
            state = AppUpdateModel::class.java.getDeclaredField("mutableState").let { field -> field.isAccessible = true; field.get(model) as MutableStateFlow<UpdateScreenState> }
            check(descendants(fragment.requireView()).filterIsInstance<TextView>().any { it.text.toString() == context.getString(R.string.update_idle) })
        }
        capture("updates-idle.png")
        for (phase in listOf(UpdatePhase.CHECKING, UpdatePhase.DOWNLOADING, UpdatePhase.VERIFYING, UpdatePhase.READY, UpdatePhase.FAILED, UpdatePhase.LATEST)) {
            instrumentation.runOnMainSync { state.value = UpdateScreenState(phase, update.takeUnless { phase == UpdatePhase.LATEST }, fixture.length() / 3,
                if (phase == UpdatePhase.FAILED) context.getString(R.string.update_network_error) else null) }
            instrumentation.waitForIdleSync(); Thread.sleep(150)
            instrumentation.runOnMainSync {
                val primary = descendants(fragment.requireView()).filterIsInstance<MaterialButton>().first()
                check(primary.isEnabled == (phase !in listOf(UpdatePhase.CHECKING, UpdatePhase.DOWNLOADING, UpdatePhase.VERIFYING)))
                check(primary.height >= 48 * context.resources.displayMetrics.density)
            }
            if (phase in listOf(UpdatePhase.DOWNLOADING, UpdatePhase.READY, UpdatePhase.FAILED)) capture("updates-${phase.name.lowercase()}.png")
        }
        // Permission navigation is exercised without launching an app replacement.
        if (android.os.Build.VERSION.SDK_INT >= 26 && !context.packageManager.canRequestPackageInstalls()) {
            instrumentation.runOnMainSync {
                state.value = UpdateScreenState(UpdatePhase.READY, update)
                AppUpdateFragment::class.java.getDeclaredMethod("openInstaller", File::class.java).let { method ->
                    method.isAccessible = true; method.invoke(fragment, fixture)
                }
            }
            fun waitForPackage(expected: String) {
                for (attempt in 0 until 50) {
                    if (instrumentation.uiAutomation.rootInActiveWindow?.packageName?.toString() == expected) return
                    Thread.sleep(100)
                }
                error("Expected $expected after installation settings navigation")
            }
            waitForPackage("com.android.settings")
            instrumentation.uiAutomation.waitForIdle(250, 5000)
            instrumentation.sendKeyDownUpSync(android.view.KeyEvent.KEYCODE_BACK)
            waitForPackage(context.packageName)
            instrumentation.runOnMainSync { check(state.value.phase == UpdatePhase.READY) }
        }
        val provider = context.packageManager.getProviderInfo(android.content.ComponentName(context, "androidx.core.content.FileProvider"), 0)
        check(!provider.exported && provider.grantUriPermissions && provider.authority == "${context.packageName}.updates")
        instrumentation.runOnMainSync { state.value = UpdateScreenState(UpdatePhase.IDLE) }
        cache.deleteRecursively()
    }
}
