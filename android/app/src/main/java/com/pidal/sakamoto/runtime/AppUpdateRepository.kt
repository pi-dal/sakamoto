package com.pidal.sakamoto.runtime

import android.content.Context
import android.content.pm.PackageInfo
import android.content.pm.PackageManager
import android.os.Build
import android.util.AtomicFile
import kotlinx.coroutines.ensureActive
import okhttp3.Call
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.IOException
import java.security.MessageDigest
import java.util.UUID
import java.util.concurrent.TimeUnit
import kotlin.coroutines.coroutineContext

/** Private cache only. Each installer is verified again before granting its content URI. */
class AppUpdateRepository(
    private val context: Context,
    private val client: OkHttpClient = githubClient(),
    private val installedOverride: (() -> PackageInfo)? = null,
    private val directory: File = File(context.cacheDir, "updates"),
) {
    private val record get() = AtomicFile(File(directory, "ready.json"))
    @Volatile private var activeCall: Call? = null
    fun cancel() { activeCall?.cancel() }

    suspend fun check(): AndroidUpdate? {
        var latest: AndroidUpdateRelease? = null
        // /latest mixes Android and iOS; inspect only this platform's stable tags.
        for (page in 1..3) {
            coroutineContext.ensureActive()
            val text = document("https://api.github.com/repos/pi-dal/sakamoto/releases?per_page=30&page=$page", 2_097_152)
            val found = AndroidUpdateRelease.latest(text)
            if (found != null && (latest == null || UpdateVersion.parse(found.versionName)!! > UpdateVersion.parse(latest.versionName)!!)) latest = found
            if (org.json.JSONArray(text).length() < 30) break
        }
        val release = latest ?: throw IOException("No stable Android release is available. Try again later or open GitHub releases.")
        val update = AndroidUpdate.select(release, document(release.assetURL("release-metadata.json"), 262_144), Build.SUPPORTED_ABIS.toList())
        if (update.versionCode <= versionCode(installed())) return null
        require(signers(installed()) == setOf(update.certificateSHA256)) {
            "This installed app uses a different signing certificate. Install the official GitHub APK manually to use in-app updates."
        }
        return update
    }

    suspend fun download(update: AndroidUpdate, progress: (Long) -> Unit): File {
        require(update.versionCode > versionCode(installed())) { "This update is already installed." }
        require(signers(installed()) == setOf(update.certificateSHA256)) { "The update signing certificate does not match this app." }
        check(directory.isDirectory || directory.mkdirs()) { "Cannot create the update cache. Free some storage and retry." }
        directory.listFiles()?.filter { it.name.endsWith(".part") }?.forEach { it.delete() }
        val partial = File(directory, "${UUID.randomUUID()}.part")
        val target = File(directory, "update.apk")
        try {
            execute(update.url).use { response ->
                checkResponse(response.code)
                val body = response.body ?: throw IOException("The installer download is empty.")
                require(body.contentLength() < 0 || body.contentLength() == update.bytes) { "The downloaded installer has an unexpected size." }
                var received = 0L
                var lastProgress = 0L
                val digest = MessageDigest.getInstance("SHA-256")
                body.byteStream().use { input -> partial.outputStream().use { output ->
                    val buffer = ByteArray(65_536)
                    while (true) {
                        coroutineContext.ensureActive()
                        val count = input.read(buffer)
                        if (count < 0) break
                        received += count
                        require(received <= update.bytes) { "The installer is larger than the release metadata allows." }
                        output.write(buffer, 0, count)
                        digest.update(buffer, 0, count)
                        val now = android.os.SystemClock.elapsedRealtime()
                        if (now - lastProgress >= 200) { progress(received); lastProgress = now }
                    }
                } }
                require(received == update.bytes && hex(digest.digest()) == update.sha256) { "Installer checksum verification failed. Download it again." }
            }
            coroutineContext.ensureActive()
            progress(update.bytes)
            verifyArchive(update, partial)
            coroutineContext.ensureActive()
            require(partial.setReadOnly()) { "Cannot protect the downloaded installer." }
            if (target.exists()) require(target.delete()) { "Cannot replace the old installer. Free some storage and retry." }
            require(partial.renameTo(target)) { "Cannot save the downloaded installer. Free some storage and retry." }
            val stream = record.startWrite()
            try { stream.write(update.toJSON().toByteArray(Charsets.UTF_8)); record.finishWrite(stream) }
            catch (error: Exception) { record.failWrite(stream); target.delete(); throw error }
            progress(update.bytes)
            return target
        } finally { partial.delete() }
    }

    suspend fun restore(): AndroidUpdate? {
        if (!File(directory, "ready.json").exists()) return null
        return try {
            val update = AndroidUpdate.fromJSON(record.readFully().toString(Charsets.UTF_8))
            verifiedInstaller(update)
            update
        } catch (error: kotlinx.coroutines.CancellationException) { throw error }
        catch (_: Exception) { record.delete(); File(directory, "update.apk").delete(); null }
    }

    suspend fun verifiedInstaller(update: AndroidUpdate): File {
        val file = File(directory, "update.apk")
        require(file.isFile && file.length() == update.bytes) { "The saved installer is missing or incomplete. Download it again." }
        val digest = MessageDigest.getInstance("SHA-256")
        file.inputStream().use { input ->
            val buffer = ByteArray(65_536)
            while (true) {
                coroutineContext.ensureActive()
                val count = input.read(buffer)
                if (count < 0) break
                digest.update(buffer, 0, count)
            }
        }
        require(hex(digest.digest()) == update.sha256) { "The saved installer failed checksum verification. Download it again." }
        verifyArchive(update, file)
        return file
    }

    @Suppress("DEPRECATION")
    private fun installed(): PackageInfo = installedOverride?.invoke() ?: context.packageManager.getPackageInfo(context.packageName, signingFlags())
    @Suppress("DEPRECATION")
    private fun verifyArchive(update: AndroidUpdate, file: File) {
        val apk = context.packageManager.getPackageArchiveInfo(file.path, signingFlags())
            ?: throw IOException("Android could not read the downloaded installer. Download it again.")
        require(apk.applicationInfo?.flags?.and(android.content.pm.ApplicationInfo.FLAG_DEBUGGABLE) == 0) { "The downloaded installer is a debug build. Open GitHub releases for an official installer." }
        val current = installed()
        update.validateArchive(apk.packageName, apk.versionName, versionCode(apk), signers(apk), versionCode(current), signers(current))
    }
    private fun execute(url: String): okhttp3.Response {
        val call = client.newCall(Request.Builder().url(url).header("Accept", "application/vnd.github+json")
            .header("User-Agent", "Sakamoto-Android-Updater").build())
        activeCall = call
        return call.execute()
    }
    private fun document(url: String, limit: Int): String {
        execute(url).use { response ->
            checkResponse(response.code)
            val body = response.body ?: throw IOException("GitHub returned an empty update response.")
            body.byteStream().use { input ->
                val output = ByteArrayOutputStream()
                val buffer = ByteArray(8192)
                while (true) {
                    val count = input.read(buffer)
                    if (count < 0) break
                    require(output.size() + count <= limit) { "The GitHub update response is too large." }
                    output.write(buffer, 0, count)
                }
                return output.toString("UTF-8")
            }
        }
    }
    private fun checkResponse(status: Int) {
        if (status == 403 || status == 429) throw IOException("GitHub limited this request. Try again later or open GitHub releases.")
        if (status !in 200..299) throw IOException("GitHub returned HTTP $status. Try again or open GitHub releases.")
    }
    companion object {
        private fun githubClient() = OkHttpClient.Builder().connectTimeout(15, TimeUnit.SECONDS).readTimeout(30, TimeUnit.SECONDS)
            .callTimeout(15, TimeUnit.MINUTES).addNetworkInterceptor { chain ->
                val url = chain.request().url
                if (!isGitHubURL(url.scheme, url.host)) throw IOException("The update download redirected outside GitHub.")
                chain.proceed(chain.request())
            }.build()
        fun isGitHubURL(scheme: String, host: String) = scheme == "https" && host in setOf("api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com")
        @Suppress("DEPRECATION")
        private fun signingFlags() = if (Build.VERSION.SDK_INT >= 28) PackageManager.GET_SIGNING_CERTIFICATES else PackageManager.GET_SIGNATURES
        @Suppress("DEPRECATION")
        private fun versionCode(info: PackageInfo): Long = if (Build.VERSION.SDK_INT >= 28) info.longVersionCode else info.versionCode.toLong()
        @Suppress("DEPRECATION")
        private fun signers(info: PackageInfo): Set<String> {
            val signatures = if (Build.VERSION.SDK_INT >= 28) info.signingInfo?.apkContentsSigners else info.signatures
            return signatures.orEmpty().map { hex(MessageDigest.getInstance("SHA-256").digest(it.toByteArray())) }.toSet()
        }
        private fun hex(bytes: ByteArray) = bytes.joinToString("") { "%02x".format(it) }
    }
}

fun AndroidUpdate.validateArchive(packageName: String, versionName: String?, code: Long, certificates: Set<String>, installedCode: Long, installedCertificates: Set<String>) {
    require(packageName == "com.pidal.sakamoto" && versionName == release.versionName && code == versionCode && code > installedCode) {
        "The installer identity or build number does not match this update. Check for updates again."
    }
    require(certificates == setOf(certificateSHA256) && certificates == installedCertificates) {
        "Installer signature verification failed. Install the official GitHub APK manually."
    }
}
