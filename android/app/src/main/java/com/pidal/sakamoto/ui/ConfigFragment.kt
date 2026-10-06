package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.snackbar.Snackbar
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import io.nekohasekai.mobilecore.Mobilecore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject

/** Index of real editing workflows with an explicit, acknowledged apply action. */
class ConfigFragment : androidx.fragment.app.Fragment() {
    private var sourceRow: GroupedPage.Row? = null
    private var stateRow: GroupedPage.Row? = null
    private var applyButton: com.google.android.material.button.MaterialButton? = null
    private var importing = false
    private var importButton: com.google.android.material.button.MaterialButton? = null
    private val packagePicker = registerForActivityResult(androidx.activity.result.contract.ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) viewLifecycleOwner.lifecycleScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    requireContext().contentResolver.openInputStream(uri)?.use { com.pidal.sakamoto.runtime.TunnelPackage.import(requireContext(), it) }
                        ?: error(getString(R.string.tunnel_package_read_failed))
                }
                render()
                view?.let { Snackbar.make(it, R.string.tunnel_package_imported, Snackbar.LENGTH_LONG).show() }
            } catch (cancelled: kotlinx.coroutines.CancellationException) { throw cancelled }
            catch (error: Exception) { view?.let { Snackbar.make(it, error.message ?: getString(R.string.profile_import_failed), Snackbar.LENGTH_LONG).show() } }
        }
    }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext())
        val source = page.section(getString(R.string.config_source_section))
        sourceRow = page.row(source, getString(R.string.source_current_conf), "", R.drawable.ic_link) { open(SourceFilesFragment(), R.string.source_files) }
        importButton = page.button(source, getString(R.string.config_import_title)) {
            EditDialogs.text(requireContext(), getString(R.string.config_import_hint), "") { url ->
                val input = url.trim()
                require(input.startsWith("https://") || input.startsWith("http://")) { getString(R.string.import_error_scheme) }
                Mobilecore.validateSourceURL(input)
                importFromUrl(input)
            }
        }
        page.row(source, getString(R.string.tunnel_package_import), getString(R.string.tunnel_package_note), R.drawable.ic_download) {
            packagePicker.launch(arrayOf("application/zip", "application/octet-stream"))
        }
        val manage = page.section(getString(R.string.config_manage_section))
        page.row(manage, getString(R.string.profiles_title), getString(R.string.profile_index_detail), R.drawable.ic_node) { open(ProfilesFragment(), R.string.profiles_title) }
        page.row(manage, getString(R.string.policy_effective_rules), getString(R.string.policy_index_detail), R.drawable.ic_route) { open(RoutingPolicyFragment(), R.string.policy_effective_rules) }
        page.row(manage, getString(R.string.proxy_chain_title), getString(R.string.proxy_chain_entry_note), R.drawable.ic_route) { open(ProxyChainFragment(), R.string.proxy_chain_title) }
        page.row(manage, getString(R.string.profile_source_title), getString(R.string.profile_sources_note), R.drawable.ic_sync) { open(SourcesManagerFragment(), R.string.profile_source_title) }
        val apply = page.section(getString(R.string.config_apply_section))
        stateRow = page.row(apply, getString(R.string.config_state_label))
        applyButton = page.button(apply, getString(R.string.apply_saved), primary = true) {
            MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.config_apply_confirm_title).setMessage(R.string.config_apply_confirm_message)
                .setNegativeButton(android.R.string.cancel, null).setPositiveButton(R.string.apply_saved) { _, _ -> apply() }.show()
        }
        page.row(apply, getString(R.string.edit_generated_config), getString(R.string.advanced_editor_note), R.drawable.ic_info) {
            EditDialogs.text(requireContext(), getString(R.string.edit_generated_config), ConfigRepository.load(requireContext()).generatedContent, multiline = true) {
                ConfigRepository.saveGeneratedEdit(requireContext(), it); render()
            }
        }
        render()
        return page.root
    }
    private fun open(fragment: androidx.fragment.app.Fragment, title: Int) { (requireActivity() as MainActivity).openChild(fragment, getString(title)) }
    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        parentFragmentManager.setFragmentResultListener("config-updated", viewLifecycleOwner) { _, _ -> render() }
        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(androidx.lifecycle.Lifecycle.State.RESUMED) { MobilecoreRuntime.state.collect { render() } }
        }
    }
    private fun render() {
        val saved = ConfigRepository.load(requireContext())
        val runtime = MobilecoreRuntime.state.value
        sourceRow?.detail(saved.sourceConfPath.ifEmpty { saved.sourceConfName }.ifEmpty { getString(R.string.config_no_source) })
        stateRow?.detail(when {
            saved.sourceNeedsGenerate -> getString(R.string.source_generation_required)
            runtime.configApplyPending -> getString(R.string.config_applying)
            runtime.notice != null -> runtime.notice
            runtime.serviceState != "Running" -> getString(R.string.config_connect_first)
            else -> runtime.configState
        })
        applyButton?.isEnabled = saved.generatedContent.isNotBlank() && !saved.sourceNeedsGenerate && runtime.serviceState == "Running" && !runtime.configApplyPending
    }
    private fun apply() {
        MobilecoreRuntime.beginConfigApply()
        render()
        viewLifecycleOwner.lifecycleScope.launch {
            val ok = withContext(Dispatchers.IO) { CommandClientRuntime.reloadService() }
            if (!ok) MobilecoreRuntime.configEvent("regenerate_failed")
            render()
        }
    }
    private fun importFromUrl(url: String) {
        if (importing) return
        importing = true; importButton?.isEnabled = false
        viewLifecycleOwner.lifecycleScope.launch {
            try {
                val content = withContext(Dispatchers.IO) {
                    val connection = java.net.URI(url).toURL().openConnection() as java.net.HttpURLConnection
                    connection.connectTimeout = 10_000; connection.readTimeout = 20_000
                    try {
                        check(connection.responseCode == 200) { "HTTP ${connection.responseCode}" }
                        connection.inputStream.use { input ->
                            val output = java.io.ByteArrayOutputStream()
                            val buffer = ByteArray(8192)
                            while (true) {
                                val count = input.read(buffer)
                                if (count < 0) break
                                require(output.size() + count <= (16 shl 20)) { "Import exceeds 16 MiB" }
                                output.write(buffer, 0, count)
                            }
                            output.toString("UTF-8")
                        }
                    } finally { connection.disconnect() }
                }
                Mobilecore.parseConfContentJSON(content)
                val current = ConfigRepository.load(requireContext())
                ConfigRepository.save(requireContext(), current.copy(sourceConfName = java.net.URI(url).path.substringAfterLast('/'), sourceConfIsUrl = true, sourceConfContent = content, sourceConfPath = "", hostSnapshotAt = "", sourceNeedsGenerate = true))
                MobilecoreRuntime.configEvent("modified")
                view?.let { Snackbar.make(it, R.string.profile_source_imported, Snackbar.LENGTH_LONG).show() }
            } catch (cancelled: kotlinx.coroutines.CancellationException) { throw cancelled }
            catch (_: Exception) { view?.let { Snackbar.make(it, R.string.profile_import_failed, Snackbar.LENGTH_LONG).show() } }
            finally { importing = false; importButton?.isEnabled = true; if (isAdded) render() }
        }
    }
    override fun onDestroyView() { sourceRow = null; stateRow = null; applyButton = null; importButton = null; super.onDestroyView() }
}
