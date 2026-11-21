package com.pidal.sakamoto.security

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey

/**
 * Storage for the Tailscale auth key — the Android counterpart of the iOS
 * Keychain store (TailscaleKeychainStore).
 *
 * Threat model and guarantees:
 *   * The key is operator input for the built-in Tailscale endpoint. It is
 *     encrypted at rest with an AES-256-GCM key that lives in the Android
 *     Keystore (hardware-backed where available) and never leaves the
 *     app-private sandbox.
 *   * Ciphertext + IV live in filesDir (MODE_PRIVATE = 0600 by policy on
 *     modern Android); nothing is written to external storage, SharedPreferences
 *     in plaintext, backups, or logs. `allowBackup=false` in the manifest.
 *   * `toString()` of this class never contains key material; no log call in
 *     this file ever receives the key.
 *
 * The key is consumed ONLY by TailscaleConfigInjection at tunnel start/reload
 * time (config travels into the tunnel process — the same trust boundary the
 * upstream clients use for profile configs).
 *
 * STATUS PENDING SDK BUILD VERIFICATION (no Android toolchain on this
 * machine) — see android/README.md. The crypto APIs used are stable since
 * API 23/24 and mirrored from the Android developer documentation.
 */
class TailscaleAuthKeyStore(private val context: Context) {

    companion object {
        private const val KEYSTORE_PROVIDER = "AndroidKeyStore"
        private const val KEY_ALIAS = "sakamoto.tailscale.authkey"
        private const val FILE_NAME = "tailscale-authkey.bin"
    }

    /** Never renders key material. */
    override fun toString(): String =
        "TailscaleAuthKeyStore(hasKey=${hasAuthKey()})"

    fun hasAuthKey(): Boolean = context.getFileStreamPath(FILE_NAME).exists()

    fun readAuthKey(): String? = readStoredCredential(context.getFileStreamPath(FILE_NAME), KEY_ALIAS)

    fun storeAuthKey(key: String) {
        val trimmed = key.trim()
        require(trimmed.isNotEmpty()) { "auth key is empty" }
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, loadOrCreateKey())
        val iv = cipher.iv
        val ciphertext = cipher.doFinal(trimmed.toByteArray(Charsets.UTF_8))
        val destination = context.getFileStreamPath(FILE_NAME)
        val staged = java.io.File.createTempFile("tailscale-authkey-", ".tmp", destination.parentFile)
        try {
            staged.outputStream().use { out ->
                out.write(iv)
                out.write(ciphertext)
                out.fd.sync()
            }
            android.system.Os.rename(staged.path, destination.path)
        } finally {
            staged.delete()
        }
    }

    fun deleteAuthKey() {
        try {
            android.system.Os.remove(context.getFileStreamPath(FILE_NAME).path)
        } catch (error: android.system.ErrnoException) {
            if (error.errno != android.system.OsConstants.ENOENT) throw error
        }
    }

    private fun loadOrCreateKey(): SecretKey {
        val keyStore = KeyStore.getInstance(KEYSTORE_PROVIDER).apply { load(null) }
        (keyStore.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE_PROVIDER)
        generator.init(
            KeyGenParameterSpec.Builder(
                KEY_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return generator.generateKey()
    }
}
