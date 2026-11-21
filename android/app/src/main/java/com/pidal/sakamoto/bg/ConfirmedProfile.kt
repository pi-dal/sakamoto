package com.pidal.sakamoto.bg

import io.nekohasekai.mobilecore.Mobilecore
import org.json.JSONObject
import java.io.File

/** Keep one complete cold-start profile reachable throughout an Apply transaction. */
internal class ConfirmedProfile(private val directory: File, private val learningCommit: () -> String?) {
    private val selection = File(directory, ".confirmed-selection")
    data class Entry(val slot: Int, val receipt: String, val connectStaged: Boolean = false)

    fun selected(): Entry? {
        val raw = try { selection.readBytes() } catch (error: java.io.FileNotFoundException) {
            if (selection.exists()) throw error
            return null
        }
        val selection = JSONObject(String(raw, Charsets.UTF_8))
        val data = if (selection.has("learningCommit") && selection.getString("learningCommit") != learningCommit())
            selection.getJSONObject("previous") else selection
        return Entry(data.getInt("slot").also { require(it in 0..1) }, data.getString("receipt"), data.optBoolean("connectStaged"))
    }

    fun directory(entry: Entry) = File(directory, ".confirmed-${entry.slot}")

    fun prepare(content: String, metadata: String, receipt: String, working: File): Entry {
        val next = Entry(1 - (selected()?.slot ?: 1), receipt)
        Mobilecore.saveApplyRollback(directory(next).absolutePath, content, metadata, working.absolutePath)
        return next
    }

    private fun Entry.json() = JSONObject().put("slot", slot).put("receipt", receipt).put("connectStaged", connectStaged)

    fun commit(next: Entry, whenLearned: String? = null) {
        val record = next.json()
        if (whenLearned != null) {
            // The manifest rename commits both the domain list and its snapshot.
            record.put("learningCommit", whenLearned).put("previous", checkNotNull(selected()).json())
        }
        val data = record.toString().toByteArray(Charsets.UTF_8)
        val tmp = File.createTempFile(".selection-", ".tmp", directory)
        try {
            tmp.outputStream().use { it.write(data); it.fd.sync() }
            android.system.Os.rename(tmp.path, selection.path)
        } finally { tmp.delete() }
    }
}
