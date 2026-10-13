package com.pidal.sakamoto

import com.pidal.sakamoto.runtime.AndroidUpdate
import com.pidal.sakamoto.runtime.AndroidUpdateRelease
import com.pidal.sakamoto.runtime.AppUpdateRepository
import com.pidal.sakamoto.runtime.UpdateVersion
import com.pidal.sakamoto.runtime.validateArchive
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class AndroidUpdateTest {
    private val hash = "a".repeat(64)
    private val signer = "b".repeat(64)
    private fun release(tag: String = "android-v0.1.13", prerelease: Boolean = false, draft: Boolean = false): JSONObject {
        val files = listOf("release-metadata.json", "sakamoto-android-v0.1.13-arm64-v8a.apk", "sakamoto-android-v0.1.13-armeabi-v7a.apk", "sakamoto-android-v0.1.13.apk")
        return JSONObject().put("tag_name", tag).put("prerelease", prerelease).put("draft", draft).put("body", "Release notes")
            .put("assets", JSONArray(files.map { file -> JSONObject().put("name", file).put("size", 100)
                .put("digest", "sha256:$hash").put("browser_download_url", "${AndroidUpdateRelease.REPOSITORY}/releases/download/$tag/$file") }))
    }
    private fun manifest(): JSONObject = JSONObject().put("versionName", "0.1.13").put("versionCode", 15).put("applicationId", "com.pidal.sakamoto")
        .put("debuggable", false).put("dirty", false).put("certificateSHA256", signer)
        .put("artifacts", JSONArray(listOf("arm64-v8a", "armeabi-v7a", "universal").map { abi ->
            JSONObject().put("abi", abi).put("file", "sakamoto-android-v0.1.13${if (abi == "universal") "" else "-$abi"}.apk")
                .put("bytes", 100).put("sha256", hash)
        }))
    private fun update(json: JSONObject = manifest(), rawRelease: JSONObject = release(), abis: List<String> = listOf("arm64-v8a", "armeabi-v7a")): AndroidUpdate =
        AndroidUpdate.select(AndroidUpdateRelease.latest(JSONArray(listOf(rawRelease)).toString())!!, json.toString(), abis)
    private fun rejects(block: () -> Unit) { assertThrows(Exception::class.java, block) }
    @Test fun stableAndroidChannelIgnoresIOSDraftsAndPrereleasesAndSortsNumerically() {
        val items = listOf(release("ios-v9.9.9"), release("android-v0.1.9"), release("android-v0.1.13"),
            release("android-v0.2.0", prerelease = true), release("android-v1.0.0", draft = true), release("android-v0.2.0-rc1"))
        assertEquals("android-v0.1.13", AndroidUpdateRelease.latest(JSONArray(items).toString())?.tag)
        assertTrue(UpdateVersion.parse("0.1.13")!! > UpdateVersion.parse("0.1.9")!!)
        assertNull(UpdateVersion.parse("0.1.9999999999999999"))
        assertNull(UpdateVersion.parse("0.1.13+dev"))
        assertNull(AndroidUpdateRelease.latest(JSONArray(listOf(release("ios-v0.1.16"))).toString()))
    }
    @Test fun installerFollowsDeviceABIOrderAndFallsBackToUniversal() {
        assertEquals("arm64-v8a", update().abi)
        assertEquals("armeabi-v7a", update(abis = listOf("armeabi-v7a", "arm64-v8a")).abi)
        assertEquals("universal", update(abis = listOf("future-abi")).abi)
        val m = manifest(); m.put("artifacts", JSONArray(listOf(m.getJSONArray("artifacts").getJSONObject(0))))
        rejects { update(json = m, abis = listOf("x86_64")) }
    }
    @Test fun rejectsMixedVersionsDirtyDebugOrForeignReleases() {
        rejects { update(manifest().put("versionName", "0.1.99")) }
        rejects { update(manifest().put("applicationId", "malicious.app")) }
        rejects { update(manifest().put("dirty", true)) }
        rejects { update(manifest().put("debuggable", true)) }
        rejects { update(manifest().put("versionCode", 0)) }
        rejects { update(manifest().put("certificateSHA256", "invalid")) }
    }
    @Test fun rejectsTamperedSizeChecksumDownloadHostAndFilename() {
        val badSize = manifest(); badSize.getJSONArray("artifacts").getJSONObject(0).put("bytes", 101); rejects { update(badSize) }
        val badHash = manifest(); badHash.getJSONArray("artifacts").getJSONObject(0).put("sha256", "c".repeat(64)); rejects { update(badHash) }
        val badFile = manifest(); badFile.getJSONArray("artifacts").getJSONObject(0).put("file", "../private.apk"); rejects { update(badFile) }
        val badHost = release(); badHost.getJSONArray("assets").getJSONObject(1).put("browser_download_url", "https://evil.example/update.apk"); rejects { update(rawRelease = badHost) }
        val duplicate = manifest(); duplicate.getJSONArray("artifacts").put(duplicate.getJSONArray("artifacts").getJSONObject(0)); rejects { update(duplicate) }
    }
    @Test fun oldGitHubAssetsWithoutAPIDigestStillRequireManifestHash() {
        val old = release(); old.getJSONArray("assets").getJSONObject(1).put("digest", JSONObject.NULL)
        assertEquals(hash, update(rawRelease = old).sha256)
    }
    @Test fun cachedCandidateRoundTripsAndArchiveMustMatchSignerIdentityAndAdvanceBuild() {
        val u = update(); val restored = AndroidUpdate.fromJSON(u.toJSON())
        assertEquals(u.url, restored.url); assertEquals(u.sha256, restored.sha256)
        restored.validateArchive("com.pidal.sakamoto", "0.1.13", 15, setOf(signer), 14, setOf(signer))
        rejects { restored.validateArchive("malicious.app", "0.1.13", 15, setOf(signer), 14, setOf(signer)) }
        rejects { restored.validateArchive("com.pidal.sakamoto", "0.1.12", 15, setOf(signer), 14, setOf(signer)) }
        rejects { restored.validateArchive("com.pidal.sakamoto", "0.1.13", 16, setOf(signer), 14, setOf(signer)) }
        rejects { restored.validateArchive("com.pidal.sakamoto", "0.1.13", 15, emptySet(), 14, setOf(signer)) }
        rejects { restored.validateArchive("com.pidal.sakamoto", "0.1.13", 15, setOf(signer), 15, setOf(signer)) }
        rejects { restored.validateArchive("com.pidal.sakamoto", "0.1.13", 15, setOf(signer), 14, setOf(hash)) }
    }
    @Test fun redirectsRequireHTTPSAndOfficialGitHubHosts() {
        assertTrue(AppUpdateRepository.isGitHubURL("https", "release-assets.githubusercontent.com"))
        assertFalse(AppUpdateRepository.isGitHubURL("http", "github.com"))
        assertFalse(AppUpdateRepository.isGitHubURL("https", "github.com.evil.example"))
        assertFalse(AppUpdateRepository.isGitHubURL("https", "raw.githubusercontent.com"))
    }
}
