package com.pidal.sakamoto.runtime

import android.content.Context
import java.io.File
import java.security.MessageDigest

/** Device-owned, immutable copies kept outside every imported profile and cloud backup. */
internal object RecoveryFiles {
    private fun root(context: Context) = File(context.noBackupFilesDir, "node-recovery")
    private val namePattern = Regex("nodes-before-sync(?:-archived-[0-9a-fA-F-]{36})?\\.txt")
    fun isRecoveryName(name: String) = namePattern.matches(name)

    fun preserve(context: Context, revision: String) {
        val source = File(ConfigRepository.packageDirectory(context, revision), "imports/local").canonicalFile
        if (!source.exists()) return
        val files = checkNotNull(source.listFiles()).filter { isRecoveryName(it.name) }
        if (files.isEmpty()) return
        val store = root(context)
        check(store.isDirectory || store.mkdirs())
        for (file in files) {
            check(file.isFile && file.canonicalFile == file)
            val temp = File.createTempFile(".saving-", null, store)
            try {
                val digest = MessageDigest.getInstance("SHA-256")
                temp.outputStream().use { output ->
                    java.security.DigestInputStream(file.inputStream(), digest).use { it.copyTo(output) }
                    output.fd.sync()
                }
                val directory = File(store, digest.digest().joinToString("") { "%02x".format(it) })
                check(directory.isDirectory || directory.mkdir())
                val copy = File(directory, file.name)
                if (!copy.exists()) check(temp.renameTo(copy)) { "Could not preserve recovery file" }
                else check(copy.isFile && copy.length() == temp.length() && java.security.DigestInputStream(copy.inputStream(), digest).use { stream ->
                    val buffer = ByteArray(8192)
                    while (stream.read(buffer) != -1) { /* verify the existing copy before discarding the revision */ }
                    digest.digest().joinToString("") { "%02x".format(it) } == directory.name
                }) { "Existing recovery file failed verification" }
            } finally { temp.delete() }
        }
    }

    fun saved(context: Context): List<File> = root(context).listFiles().orEmpty().filter { it.isDirectory && it.name.matches(Regex("[0-9a-f]{64}")) }
        .flatMap { it.listFiles().orEmpty().filter { file -> file.isFile && isRecoveryName(file.name) } }.sortedBy { it.path }

    fun key(file: File) = "${file.parentFile!!.name}/${file.name}"
}
