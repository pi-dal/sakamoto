package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.lifecycle.lifecycleScope
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.RecoveryFiles
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File

/** Browse the actual host source snapshot, including nested conf dependencies. */
class SourceFilesFragment : androidx.fragment.app.Fragment() {
    private var exportKey: String? = null
    private val saveCopy = registerForActivityResult(androidx.activity.result.contract.ActivityResultContracts.CreateDocument("text/plain")) { uri ->
        val key = exportKey
        exportKey = null
        if (uri != null && key != null) lifecycleScope.launch {
            val context = requireContext().applicationContext
            try {
                withContext(Dispatchers.IO) {
                    require(uri.scheme == "content")
                    val file = RecoveryFiles.saved(context).single { RecoveryFiles.key(it) == key }
                    checkNotNull(context.contentResolver.openOutputStream(uri, "wt")).use { output -> file.inputStream().use { it.copyTo(output) } }
                }
            } catch (cancelled: kotlinx.coroutines.CancellationException) { throw cancelled }
            catch (error: Exception) { if (isAdded) MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.edit_failed).setMessage(error.message).setPositiveButton(android.R.string.ok, null).show() }
        }
    }

    override fun onCreate(state: Bundle?) { super.onCreate(state); exportKey = state?.getString("exportRecoveryKey") }
    override fun onSaveInstanceState(outState: Bundle) { super.onSaveInstanceState(outState); outState.putString("exportRecoveryKey", exportKey) }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val container = android.widget.FrameLayout(requireContext())
        render(container)
        return container
    }

    private fun render(container: ViewGroup) {
        val page = GroupedPage(requireContext())
        val staged = ConfigRepository.load(requireContext())
        val overview = page.section(getString(R.string.source_snapshot))
        page.row(overview, getString(R.string.source_current_conf), staged.sourceConfPath.ifEmpty { staged.sourceConfName }) {
            val options = ConfigRepository.sourceFiles(requireContext()).filter { it.extension == "conf" }
            val base = File(ConfigRepository.packageDirectory(requireContext(), staged.packageRevision), "imports/local")
            EditDialogs.choice(requireContext(), getString(R.string.source_current_conf), options.map { it.relativeTo(base).path }, staged.sourceConfPath) { selected ->
                val file = options.first { it.relativeTo(base).path == selected }
                val content = file.readText()
                io.nekohasekai.mobilecore.Mobilecore.parseConfContentJSON(content)
                val current = ConfigRepository.load(requireContext())
                check(current == staged) { "Configuration changed. Reopen Sources." }
                ConfigRepository.save(requireContext(), current.copy(sourceConfName = file.name, sourceConfPath = selected, sourceConfContent = content, sourceConfIsUrl = false, sourceNeedsGenerate = true), staged)
                com.pidal.sakamoto.runtime.MobilecoreRuntime.configEvent("modified")
                render(container)
                parentFragmentManager.setFragmentResult("config-updated", Bundle())
            }
        }
        page.row(overview, getString(R.string.source_sync_time), staged.hostSnapshotAt.ifEmpty { getString(R.string.source_time_unknown) })
        page.note(getString(R.string.source_snapshot_note))
        val files = ConfigRepository.sourceFiles(requireContext())
        val group = page.section(getString(R.string.source_files))
        val base = File(ConfigRepository.packageDirectory(requireContext(), staged.packageRevision), "imports/local")
        for (file in files) {
            val name = file.relativeTo(base).path
            page.row(group, name, getString(R.string.source_file_size, file.length()), R.drawable.ic_link) {
                viewLifecycleOwner.lifecycleScope.launch {
                    val nodeText = if (name == "nodes.txt") withContext(Dispatchers.IO) {
                        ConfigRepository.nodeSourceText(requireContext(), staged).orEmpty()
                    } else null
                    val byteCount = nodeText?.toByteArray()?.size?.toLong() ?: file.length()
                    val detail = withContext(Dispatchers.IO) {
                        val count = nodeText?.reader()?.buffered()?.useLines { it.count() } ?: file.bufferedReader().useLines { it.count() }
                        // Source conf/node lists can contain credentials.
                        // Show file facts first; content requires explicit reveal.
                        getString(R.string.source_file_info, count, byteCount)
                    }
                    val recovery = name == "nodes-before-sync.txt"
                    val message = if (recovery) "$detail\n\n" + getString(R.string.source_archive_hint) else detail
                    MaterialAlertDialogBuilder(requireContext()).setTitle(name).setMessage(message)
                        .setNegativeButton(android.R.string.cancel, null)
                        .setPositiveButton(R.string.source_open_preview) { _, _ ->
                            viewLifecycleOwner.lifecycleScope.launch {
                                val preview = nodeText?.take(24_000) ?: withContext(Dispatchers.IO) { file.bufferedReader().use { it.readText().take(24_000) } }
                                val text = page.text(preview).apply { setTextIsSelectable(true); setPadding(page.dp(20), page.dp(12), page.dp(20), page.dp(12)) }
                                val scroll = androidx.core.widget.NestedScrollView(requireContext()).apply { addView(text) }
                                MaterialAlertDialogBuilder(requireContext()).setTitle(name).setView(scroll).setPositiveButton(android.R.string.ok, null).show()
                            }
                        }.setNeutralButton(if (recovery) R.string.source_archive_copy else R.string.edit) { _, _ ->
                            if (recovery) viewLifecycleOwner.lifecycleScope.launch {
                                val context = requireContext().applicationContext
                                try {
                                    withContext(Dispatchers.IO) {
                                        ConfigRepository.saveSourceRevision(context, staged, staged, emptyMap(), archiveRecovery = true)
                                    }
                                    render(container)
                                } catch (cancelled: kotlinx.coroutines.CancellationException) { throw cancelled }
                                catch (error: Exception) {
                                    MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.edit_failed).setMessage(error.message).setPositiveButton(android.R.string.ok, null).show()
                                }
                            } else if (file.extension != "conf" && file.name != "nodes.txt") {
                                MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.source_snapshot).setMessage(R.string.source_metadata_readonly).setPositiveButton(android.R.string.ok, null).show()
                            } else if (byteCount > 256_000) {
                                MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.edit_failed).setMessage(R.string.source_edit_limit).setPositiveButton(android.R.string.ok, null).show()
                            } else EditDialogs.text(requireContext(), name, nodeText ?: file.readText(), multiline = true) { edited ->
                                ConfigRepository.editSource(requireContext(), staged, file, edited)
                                render(container)
                                parentFragmentManager.setFragmentResult("config-updated", Bundle())
                            }
                        }.show()
                }
            }
        }
        val retained = RecoveryFiles.saved(requireContext())
        if (retained.isNotEmpty()) {
            val previous = page.section(getString(R.string.previous_recovery_files))
            for (copy in retained) {
                val label = "${copy.name} · ${copy.parentFile!!.name.take(12)}"
                page.row(previous, label, getString(R.string.source_file_size, copy.length()), R.drawable.ic_link) {
                    MaterialAlertDialogBuilder(requireContext()).setTitle(copy.name).setMessage(R.string.export_recovery_hint)
                        .setNegativeButton(android.R.string.cancel, null).setPositiveButton(R.string.export_recovery_copy) { _, _ ->
                            exportKey = RecoveryFiles.key(copy)
                            saveCopy.launch(copy.name)
                        }.show()
                }
            }
        }
        if (files.isEmpty()) page.row(group, getString(R.string.source_no_files))
        val rules = File(ConfigRepository.packageDirectory(requireContext(), staged.packageRevision), "rules").listFiles()?.filter { it.extension == "srs" }.orEmpty()
        val compiled = page.section(getString(R.string.source_compiled_rules))
        for (file in rules.sortedBy { it.name }) page.row(compiled, file.name, getString(R.string.source_file_size, file.length()), R.drawable.ic_route)
        container.removeAllViews()
        container.addView(page.root)
    }
}
