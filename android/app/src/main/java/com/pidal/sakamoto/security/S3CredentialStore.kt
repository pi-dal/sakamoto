package com.pidal.sakamoto.security

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.io.File
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey

/** S3 credentials encrypted with a device-local Android Keystore AES-GCM key. */
class S3CredentialStore(private val context: Context) {

    companion object {
        private const val KEYSTORE_PROVIDER = "AndroidKeyStore"
        private const val KEY_ALIAS = "sakamoto.s3.credentials"
        private const val FILE_NAME = "s3-credentials.bin"
    }

    /** Never renders key material. */
    override fun toString(): String =
        "S3CredentialStore(hasKey=${hasAuthKey()})"

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
        val staged = File.createTempFile("s3-credentials-", ".tmp", destination.parentFile)
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
