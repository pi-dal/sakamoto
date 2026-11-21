package com.pidal.sakamoto

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.net.Uri
import android.os.Binder
import android.os.Bundle
import com.pidal.sakamoto.runtime.TunnelPackage
import java.io.File
import java.security.MessageDigest
import java.util.UUID

/** Shell-only entry for the debug USB helper. The package importer owns publication. */
class DebugHostImport : ContentProvider() {
    override fun onCreate() = true

    override fun call(method: String, arg: String?, extras: Bundle?): Bundle {
        check(Binder.getCallingUid() in setOf(0, 2000))
        require(method == "install" && arg != null && UUID.fromString(arg).toString() == arg)
        val ctx = requireNotNull(context)
        val archive = File(ctx.cacheDir, "host-sync-$arg.zip")
        val manifest = File(ctx.filesDir, "sakamoto-config.json")
        fun digest(file: File): String {
            val hash = MessageDigest.getInstance("SHA-256")
            file.inputStream().use { input ->
                val buffer = ByteArray(8192)
                while (true) {
                    val count = input.read(buffer)
                    if (count < 0) break
                    hash.update(buffer, 0, count)
                }
            }
            return hash.digest().joinToString("") { "%02x".format(it) }
        }
        try {
            require(digest(archive) == extras?.getString("sha256")) { "Incomplete USB transfer" }
            archive.inputStream().use { stream ->
                TunnelPackage.import(ctx, stream) {
                    val current = if (manifest.exists()) digest(manifest) else "absent"
                    check(current == extras?.getString("base")) { "Device configuration changed during sync" }
                }
            }
            return Bundle().apply { putString("installed", arg) }
        } finally { archive.delete() }
    }

    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor? = null
    override fun insert(uri: Uri, values: ContentValues?): Uri? = null
    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?) = 0
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?) = 0
    override fun getType(uri: Uri): String? = null
}
