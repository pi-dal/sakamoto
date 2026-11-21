package com.pidal.sakamoto.security

import android.system.ErrnoException
import android.system.Os
import android.system.OsConstants
import java.io.File
import java.io.FileInputStream
import java.io.IOException
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** Only a missing ciphertext means the user has no saved credential. */
internal fun readStoredCredential(file: File, alias: String): String? {
    val fd = try {
        Os.open(file.path, OsConstants.O_RDONLY, 0)
    } catch (e: ErrnoException) {
        if (e.errno == OsConstants.ENOENT) return null
        throw IOException("Could not read the saved credential", e)
    }
    try {
        val blob = FileInputStream(fd).use { it.readBytes() }
        require(blob.size > 12)
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        val key = store.getKey(alias, null) as SecretKey
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, blob, 0, 12))
        return String(cipher.doFinal(blob, 12, blob.size - 12), Charsets.UTF_8).also { require(it.isNotEmpty()) }
    } catch (e: Exception) {
        throw IOException("Could not read the saved credential", e)
    } finally {
        Os.close(fd)
    }
}
