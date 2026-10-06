package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.lifecycle.lifecycleScope
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.ConfigRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File

/** Browse the actual host source snapshot, including nested conf dependencies. */
class SourceFilesFragment : androidx.fragment.app.Fragment() {
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext())
        val staged = ConfigRepository.load(requireContext())
        val overview = page.section(getString(R.string.source_snapshot))
        page.row(overview, getString(R.string.source_current_conf), staged.sourceConfPath.ifEmpty { staged.sourceConfName }) {
            val options = ConfigRepository.sourceFiles(requireContext()).filter { it.extension == "conf" }
            val base = File(requireContext().filesDir, "imports/local")
            EditDialogs.choice(requireContext(), getString(R.string.source_current_conf), options.map { it.relativeTo(base).path }, staged.sourceConfPath) { selected ->
                val file = options.first { it.relativeTo(base).path == selected }
                val content = file.readText()
                io.nekohasekai.mobilecore.Mobilecore.parseConfContentJSON(content)
                val current = ConfigRepository.load(requireContext())
                ConfigRepository.save(requireContext(), current.copy(sourceConfName = file.name, sourceConfPath = selected, sourceConfContent = content, sourceConfIsUrl = false, sourceNeedsGenerate = true))
                com.pidal.sakamoto.runtime.MobilecoreRuntime.configEvent("modified")
                parentFragmentManager.setFragmentResult("config-updated", Bundle())
            }
        }
        page.row(overview, getString(R.string.source_sync_time), staged.hostSnapshotAt.ifEmpty { getString(R.string.source_time_unknown) })
        page.note(getString(R.string.source_snapshot_note))
        val files = ConfigRepository.sourceFiles(requireContext())
        val group = page.section(getString(R.string.source_files))
        val base = File(requireContext().filesDir, "imports/local")
        for (file in files) {
            val name = file.relativeTo(base).path
            page.row(group, name, getString(R.string.source_file_size, file.length()), R.drawable.ic_link) {
                viewLifecycleOwner.lifecycleScope.launch {
                    val detail = withContext(Dispatchers.IO) {
                        val count = file.bufferedReader().useLines { it.count() }
                        // Source conf/node lists can contain credentials.
                        // Show file facts first; content requires explicit reveal.
                        getString(R.string.source_file_info, count, file.length())
                    }
                    MaterialAlertDialogBuilder(requireContext()).setTitle(name).setMessage(detail)
                        .setNegativeButton(android.R.string.cancel, null)
                        .setPositiveButton(R.string.source_open_preview) { _, _ ->
                            viewLifecycleOwner.lifecycleScope.launch {
                                val preview = withContext(Dispatchers.IO) { file.bufferedReader().use { it.readText().take(24_000) } }
                                val text = page.text(preview).apply { setTextIsSelectable(true); setPadding(page.dp(20), page.dp(12), page.dp(20), page.dp(12)) }
                                val scroll = androidx.core.widget.NestedScrollView(requireContext()).apply { addView(text) }
                                MaterialAlertDialogBuilder(requireContext()).setTitle(name).setView(scroll).setPositiveButton(android.R.string.ok, null).show()
                            }
                        }.setNeutralButton(R.string.edit) { _, _ ->
                            if (file.extension != "conf" && file.name != "nodes.txt") {
                                MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.source_snapshot).setMessage(R.string.source_metadata_readonly).setPositiveButton(android.R.string.ok, null).show()
                            } else if (file.length() > 256_000) {
                                MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.edit_failed).setMessage(R.string.source_edit_limit).setPositiveButton(android.R.string.ok, null).show()
                            } else EditDialogs.text(requireContext(), name, file.readText(), multiline = true) { edited ->
                                if (file.extension == "conf") io.nekohasekai.mobilecore.Mobilecore.parseConfContentJSON(edited)
                                else if (file.name == "nodes.txt") edited.lines().map { it.trim() }.filter { it.isNotEmpty() && !it.startsWith("#") }.forEach { io.nekohasekai.mobilecore.Mobilecore.parseShareLink(it) }
                                else org.json.JSONTokener(edited).nextValue()
                                val temp = File(file.parentFile, file.name + ".edit.tmp")
                                temp.writeText(edited)
                                check(temp.renameTo(file)) { "Unable to save source" }
                                val current = ConfigRepository.load(requireContext())
                                val relative = file.relativeTo(base).path
                                if (file.name == "nodes.txt") ConfigRepository.save(requireContext(), current.copy(sourceNeedsGenerate = true, nodes = edited.lines().map { it.trim() }.filter { it.isNotEmpty() && !it.startsWith("#") }))
                                else ConfigRepository.save(requireContext(), current.copy(sourceConfContent = if (current.sourceConfPath == relative) edited else current.sourceConfContent, sourceNeedsGenerate = true))
                                com.pidal.sakamoto.runtime.MobilecoreRuntime.configEvent("modified")
                                parentFragmentManager.setFragmentResult("config-updated", Bundle())
                            }
                        }.show()
                }
            }
        }
        if (files.isEmpty()) page.row(group, getString(R.string.source_no_files))
        val rules = File(requireContext().filesDir, "rules").listFiles()?.filter { it.extension == "srs" }.orEmpty()
        val compiled = page.section(getString(R.string.source_compiled_rules))
        for (file in rules.sortedBy { it.name }) page.row(compiled, file.name, getString(R.string.source_file_size, file.length()), R.drawable.ic_route)
        return page.root
    }
}
