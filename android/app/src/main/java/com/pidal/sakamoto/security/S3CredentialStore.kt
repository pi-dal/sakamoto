package com.pidal.sakamoto.security

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** S3 credentials encrypted with a device-local Android Keystore AES-GCM key. */
class S3CredentialStore(private val context: Context) {

    companion object {
        private const val KEYSTORE_PROVIDER = "AndroidKeyStore"
        private const val KEY_ALIAS = "sakamoto.s3.credentials"
        private const val FILE_NAME = "s3-credentials.bin"
        private const val GCM_IV_BYTES = 12
        private const val GCM_TAG_BITS = 128
    }

    /** Never renders key material. */
    override fun toString(): String =
        "S3CredentialStore(hasKey=${hasAuthKey()})"

    fun hasAuthKey(): Boolean = context.getFileStreamPath(FILE_NAME).exists()

    fun readAuthKey(): String? {
        val file = context.getFileStreamPath(FILE_NAME)
        if (!file.exists()) return null
        val blob = try {
            context.openFileInput(FILE_NAME).use { it.readBytes() }
        } catch (e: Exception) {
            return null
        }
        if (blob.size <= GCM_IV_BYTES) return null
        val iv = blob.copyOfRange(0, GCM_IV_BYTES)
        val ciphertext = blob.copyOfRange(GCM_IV_BYTES, blob.size)
        return try {
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.DECRYPT_MODE, loadOrCreateKey(), GCMParameterSpec(GCM_TAG_BITS, iv))
            String(cipher.doFinal(ciphertext), Charsets.UTF_8)
        } catch (e: Exception) {
            // Wrong key / tampered file: treat as absent; the caller falls
            // back to the auth-URL login flow. Never log the payload.
            null
        }
    }

    fun storeAuthKey(key: String) {
        val trimmed = key.trim()
        require(trimmed.isNotEmpty()) { "auth key is empty" }
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, loadOrCreateKey())
        val iv = cipher.iv
        val ciphertext = cipher.doFinal(trimmed.toByteArray(Charsets.UTF_8))
        context.openFileOutput(FILE_NAME, Context.MODE_PRIVATE).use { out ->
            out.write(iv)
            out.write(ciphertext)
        }
    }

    fun deleteAuthKey() {
        context.deleteFile(FILE_NAME)
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
