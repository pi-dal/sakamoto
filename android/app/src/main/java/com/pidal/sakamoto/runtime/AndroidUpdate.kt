package com.pidal.sakamoto.runtime

import org.json.JSONArray
import org.json.JSONObject

/** Only public, stable Android tags belong to the APK update channel. */
data class UpdateVersion(val parts: List<Int>) : Comparable<UpdateVersion> {
    override fun compareTo(other: UpdateVersion): Int {
        for (index in 0..2) {
            val result = parts[index].compareTo(other.parts[index])
            if (result != 0) return result
        }
        return 0
    }
    companion object {
        fun parse(text: String): UpdateVersion? {
            if (!text.matches(Regex("[0-9]+\\.[0-9]+\\.[0-9]+"))) return null
            val parts = text.split('.').map { it.toIntOrNull() ?: return null }
            return UpdateVersion(parts)
        }
    }
}

data class AndroidUpdateRelease(val tag: String, val notes: String, val assets: Map<String, JSONObject>) {
    val versionName: String get() = tag.removePrefix("android-v")
    val pageURL: String get() = "$REPOSITORY/releases/tag/$tag"
    fun assetURL(file: String): String {
        require(file.matches(Regex("[a-zA-Z0-9._-]+"))) { "Invalid update filename." }
        val expected = "$REPOSITORY/releases/download/$tag/$file"
        require(assets[file]?.optString("browser_download_url") == expected) { "The release has no valid $file download." }
        return expected
    }
    companion object {
        const val REPOSITORY = "https://github.com/pi-dal/sakamoto"
        fun latest(json: String): AndroidUpdateRelease? {
            val list = JSONArray(json)
            return (0 until list.length()).mapNotNull { index ->
                val item = list.getJSONObject(index)
                val tag = item.optString("tag_name")
                if (item.optBoolean("draft", true) || item.optBoolean("prerelease", true) || !tag.startsWith("android-v") ||
                    UpdateVersion.parse(tag.removePrefix("android-v")) == null) return@mapNotNull null
                val assets = item.optJSONArray("assets") ?: JSONArray()
                AndroidUpdateRelease(tag, item.optString("body").take(12_000), (0 until assets.length()).associate {
                    val asset = assets.getJSONObject(it)
                    asset.getString("name") to asset
                })
            }.maxByOrNull { UpdateVersion.parse(it.versionName)!! }
        }
    }
}

data class AndroidUpdate(
    val release: AndroidUpdateRelease, val versionCode: Long, val file: String, val abi: String,
    val bytes: Long, val sha256: String, val certificateSHA256: String,
) {
    val url: String get() = release.assetURL(file)
    fun toJSON(): String = JSONObject().put("release", JSONObject().put("tag", release.tag).put("notes", release.notes)
        .put("assets", JSONObject(release.assets))).put("versionCode", versionCode).put("file", file).put("abi", abi)
        .put("bytes", bytes).put("sha256", sha256).put("certificateSHA256", certificateSHA256).toString()

    companion object {
        fun select(release: AndroidUpdateRelease, metadata: String, supportedABIs: List<String>): AndroidUpdate {
            val manifest = JSONObject(metadata)
            require(manifest.getString("applicationId") == "com.pidal.sakamoto" && !manifest.getBoolean("debuggable") && !manifest.getBoolean("dirty")) { "This is not a verified Sakamoto release." }
            require(manifest.getString("versionName") == release.versionName) { "Release version and installer metadata disagree." }
            val code = manifest.getLong("versionCode")
            require(code in 1..2_100_000_000L) { "Invalid release build number." }
            val artifacts = manifest.getJSONArray("artifacts")
            val files = (0 until artifacts.length()).map { artifacts.getJSONObject(it) }
            require(files.map { it.getString("abi") }.distinct().size == files.size) { "Duplicate installer architecture." }
            val artifact = (supportedABIs + "universal").firstNotNullOfOrNull { abi -> files.firstOrNull { it.getString("abi") == abi } }
                ?: error("This release has no installer for this device.")
            val abi = artifact.getString("abi")
            val suffix = if (abi == "universal") "" else "-$abi"
            val file = artifact.getString("file")
            require(file == "sakamoto-android-v${release.versionName}$suffix.apk") { "Unexpected installer filename." }
            release.assetURL(file)
            val bytes = artifact.getLong("bytes")
            require(bytes in 1..536_870_912L && release.assets[file]?.getLong("size") == bytes) { "Installer size and release metadata disagree." }
            val hash = artifact.getString("sha256").lowercase()
            val certificate = manifest.getString("certificateSHA256").lowercase()
            require(hash.matches(Regex("[0-9a-f]{64}")) && certificate.matches(Regex("[0-9a-f]{64}"))) { "Invalid release checksum or signing certificate." }
            val digest = release.assets[file]?.optString("digest", "")?.takeUnless { it == "null" }
            require(digest.isNullOrEmpty() || digest == "sha256:$hash") { "GitHub and release checksums disagree." }
            return AndroidUpdate(release, code, file, abi, bytes, hash, certificate)
        }
        fun fromJSON(text: String): AndroidUpdate {
            val value = JSONObject(text)
            val release = value.getJSONObject("release")
            val assets = release.getJSONObject("assets")
            val tag = release.getString("tag")
            require(tag.startsWith("android-v") && UpdateVersion.parse(tag.removePrefix("android-v")) != null)
            return AndroidUpdate(AndroidUpdateRelease(tag, release.getString("notes"), assets.keys().asSequence().associateWith { assets.getJSONObject(it) }),
                value.getLong("versionCode"), value.getString("file"), value.getString("abi"), value.getLong("bytes"), value.getString("sha256"), value.getString("certificateSHA256"))
        }
    }
}
