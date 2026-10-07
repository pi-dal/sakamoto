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
    private var sourceRow: GroupedPage.SettingRow? = null
    private var stateRow: GroupedPage.SettingRow? = null
    private var applyButton: GroupedPage.SettingRow? = null
    private var importing = false
    private var importButton: GroupedPage.SettingRow? = null
    private val clipboardGate = ClipboardImportGate()
    private var clipboardOffer: Snackbar? = null
    private val clipboardFocusListener = android.view.ViewTreeObserver.OnWindowFocusChangeListener { focused ->
        if (focused && lifecycle.currentState.isAtLeast(androidx.lifecycle.Lifecycle.State.RESUMED)) probeClipboard()
    }
    private val qrScanner = registerForActivityResult(com.journeyapps.barcodescanner.ScanContract()) { result ->
        if (result.originalIntent?.getBooleanExtra(com.google.zxing.client.android.Intents.Scan.MISSING_CAMERA_PERMISSION, false) == true) importFeedback(R.string.ux_import_camera_denied)
        else result.contents?.let { routeImport(it) }
    }
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
        val source = page.section()
        sourceRow = page.setting(source, getString(R.string.source_current_conf), icon = R.drawable.ic_link) { open(SourceFilesFragment(), R.string.source_files) }
        importButton = page.setting(source, getString(R.string.config_import_title), icon = R.drawable.ic_download) {
            EditDialogs.text(requireContext(), getString(R.string.config_import_hint), "") { url ->
                val input = url.trim()
                require(input.startsWith("https://") || input.startsWith("http://")) { getString(R.string.import_error_scheme) }
                Mobilecore.validateSourceURL(input)
                importFromUrl(input)
            }
        }
        page.setting(source, getString(R.string.ux_import_clipboard), icon = R.drawable.ic_download) {
            val clipboard = requireContext().getSystemService(android.content.ClipboardManager::class.java)
            val text = clipboard.primaryClip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.text?.toString()
            if (text == null) importFeedback(R.string.ux_import_invalid) else routeImport(text)
        }
        page.setting(source, getString(R.string.ux_import_qr), icon = R.drawable.ic_node) {
            qrScanner.launch(com.journeyapps.barcodescanner.ScanOptions().apply {
                setDesiredBarcodeFormats(com.journeyapps.barcodescanner.ScanOptions.QR_CODE)
                setPrompt(getString(R.string.ux_import_scan_prompt))
                setBeepEnabled(false)
                setOrientationLocked(false)
                setBarcodeImageEnabled(false)
            })
        }
        page.setting(source, getString(R.string.tunnel_package_import), icon = R.drawable.ic_download) {
            packagePicker.launch(arrayOf("application/zip", "application/octet-stream"))
        }
        val manage = page.section(getString(R.string.config_manage_section))
        page.setting(manage, getString(R.string.profiles_title), icon = R.drawable.ic_node) { open(ProfilesFragment(), R.string.profiles_title) }
        page.setting(manage, getString(R.string.policy_effective_rules), icon = R.drawable.ic_route) { open(RoutingPolicyFragment(), R.string.policy_effective_rules) }
        page.setting(manage, getString(R.string.proxy_chain_title), icon = R.drawable.ic_route) { open(ProxyChainFragment(), R.string.proxy_chain_title) }
        page.setting(manage, getString(R.string.profile_source_title), icon = R.drawable.ic_sync) { open(SourcesManagerFragment(), R.string.profile_source_title) }
        val apply = page.section(getString(R.string.config_apply_section))
        stateRow = page.setting(apply, getString(R.string.config_state_label)) {
            val saved = ConfigRepository.load(requireContext())
            val runtime = MobilecoreRuntime.state.value
            val detail = when {
                saved.sourceNeedsGenerate -> getString(R.string.source_generation_required)
                runtime.configApplyPending -> getString(R.string.config_applying)
                runtime.notice != null -> runtime.notice
                runtime.serviceState != "Running" -> getString(R.string.config_connect_first)
                else -> runtime.configState
            }
            MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.config_state_label)
                .setMessage(detail).setPositiveButton(android.R.string.ok, null).show()
        }
        applyButton = page.setting(apply, getString(R.string.apply_saved)) {
            MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.config_apply_confirm_title).setMessage(R.string.config_apply_confirm_message)
                .setNegativeButton(android.R.string.cancel, null).setPositiveButton(R.string.apply_saved) { _, _ -> apply() }.show()
        }
        page.setting(apply, getString(R.string.edit_generated_config)) {
            EditDialogs.text(requireContext(), getString(R.string.edit_generated_config), ConfigRepository.load(requireContext()).generatedContent, multiline = true) {
                ConfigRepository.saveGeneratedEdit(requireContext(), it); render()
            }
        }
        render()
        return page.root
    }
    private fun importFeedback(message: Int) { view?.let { Snackbar.make(it, message, Snackbar.LENGTH_LONG).show() } }

    override fun onResume() {
        super.onResume()
        view?.post {
            if (lifecycle.currentState.isAtLeast(androidx.lifecycle.Lifecycle.State.RESUMED) && view?.hasWindowFocus() == true) probeClipboard()
        }
    }

    override fun onPause() {
        // Leaving the tab/app mid-offer ends it without suppressing the
        // payload — the seen stamp alone prevents a re-read/re-offer.
        clipboardOffer?.dismiss()
        super.onPause()
    }

    /**
     * Foreground auto-detection: on every RESUMED transition (entering Config
     * or returning to the app) the clipboard is read once, classified, and a
     * dismissible import prompt is offered. Reading while RESUMED is the only
     * reliable window — Android withholds clipboard access from non-default-IME
     * apps unless they are in the foreground. Nothing is auto-saved and the
     * raw payload is never displayed or logged — only its kind label shows,
     * and a SHA-256 identity dedupes repeats across lifecycle transitions.
     */
    private fun probeClipboard() {
        if (view?.hasWindowFocus() != true) return
        val context = context ?: return
        val clipboard = context.getSystemService(android.content.ClipboardManager::class.java) ?: return
        val text = clipboard.primaryClip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.text?.toString() ?: return
        val candidate = try { ImportPayload.detect(text) } catch (_: Exception) { return }
        val identity = clipboardIdentity(candidate.text)
        if (clipboardGate.decide(identity) != ClipboardImportGate.Decision.OFFER) return
        showClipboardOffer(candidate, identity)
    }

    /** Stable content key for dedup — clipboard text never persists in memory beyond the route. */
    private fun clipboardIdentity(text: String): String =
        java.security.MessageDigest.getInstance("SHA-256")
            .digest(text.toByteArray(Charsets.UTF_8)).joinToString("") { "%02x".format(it) }

    private fun showClipboardOffer(candidate: ImportPayload.Candidate, identity: String) {
        val view = view ?: return
        val message = when (candidate.kind) {
            ImportPayload.Kind.NODE -> R.string.ux_clipboard_offer_node
            ImportPayload.Kind.URL -> R.string.ux_clipboard_offer_url
            ImportPayload.Kind.CONFIG_JSON, ImportPayload.Kind.CONF -> R.string.ux_clipboard_offer_config
        }
        clipboardGate.offerShown()
        val snackbar = Snackbar.make(view, message, Snackbar.LENGTH_LONG)
            .setAction(R.string.ux_clipboard_import) {
                clipboardGate.userResolved(identity)
                routeImport(candidate.text)
            }
            .addCallback(object : Snackbar.Callback() {
                override fun onDismissed(dismissed: Snackbar, event: Int) {
                    if (dismissed !== clipboardOffer) return
                    clipboardOffer = null
                    if (event == Snackbar.Callback.DISMISS_EVENT_MANUAL) clipboardGate.navigationResolved()
                    else clipboardGate.userResolved(identity)
                }
            })
        clipboardOffer?.dismiss()
        clipboardOffer = snackbar
        snackbar.show()
    }

    private fun routeImport(raw: String) {
        try {
            val candidate = ImportPayload.detect(raw)
            when (candidate.kind) {
                ImportPayload.Kind.NODE -> {
                    Mobilecore.parseShareLink(candidate.text)
                    AddNodeSheet().apply { arguments = Bundle().apply { putString("prefill", candidate.text) } }
                        .show(parentFragmentManager, "import-node")
                }
                ImportPayload.Kind.URL -> MaterialAlertDialogBuilder(requireContext())
                    .setTitle(R.string.ux_import_url_title)
                    .setItems(arrayOf(getString(R.string.ux_import_url_config), getString(R.string.ux_import_url_subscription))) { _, index ->
                        if (index == 0) importFromUrl(candidate.text)
                        else AddSubscriptionSheet().apply {
                            arguments = Bundle().apply { putString("prefill", candidate.text) }
                        }.show(parentFragmentManager, "import-subscription")
                    }.setNegativeButton(android.R.string.cancel, null).show()
                ImportPayload.Kind.CONFIG_JSON, ImportPayload.Kind.CONF -> {
                    if (candidate.kind == ImportPayload.Kind.CONFIG_JSON) {
                        Mobilecore.validateConfigJSON(candidate.text)
                        io.nekohasekai.libbox.Libbox.checkConfig(candidate.text)
                    } else Mobilecore.parseConfContentJSON(candidate.text)
                    MaterialAlertDialogBuilder(requireContext())
                        .setTitle(if (candidate.kind == ImportPayload.Kind.CONFIG_JSON) R.string.ux_import_confirm_json else R.string.ux_import_confirm_conf)
                        .setMessage(R.string.ux_import_confirm_message)
                        .setNegativeButton(android.R.string.cancel, null)
                        .setPositiveButton(R.string.save) { _, _ ->
                            try {
                                if (candidate.kind == ImportPayload.Kind.CONFIG_JSON) ConfigRepository.saveGeneratedEdit(requireContext(), candidate.text)
                                else {
                                    val current = ConfigRepository.load(requireContext())
                                    ConfigRepository.save(requireContext(), current.copy(sourceConfContent = candidate.text,
                                        sourceConfName = getString(R.string.ux_import_source_name), sourceConfIsUrl = false,
                                        sourceConfPath = "", hostSnapshotAt = "", sourceNeedsGenerate = true))
                                    MobilecoreRuntime.configEvent("modified")
                                }
                                render(); importFeedback(R.string.ux_import_saved)
                            } catch (_: Exception) { importFeedback(R.string.ux_import_invalid) }
                        }.show()
                }
            }
        } catch (_: Exception) { importFeedback(R.string.ux_import_invalid) }
    }

    private fun open(fragment: androidx.fragment.app.Fragment, title: Int) { (requireActivity() as MainActivity).openChild(fragment, getString(title)) }
    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        view.viewTreeObserver.addOnWindowFocusChangeListener(clipboardFocusListener)
        state?.let { clipboardGate.restore(it.getStringArrayList("clipboardHandled") ?: emptyList(), it.getString("clipboardLast")) }
        parentFragmentManager.setFragmentResultListener("config-updated", viewLifecycleOwner) { _, _ -> render() }
        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(androidx.lifecycle.Lifecycle.State.RESUMED) { MobilecoreRuntime.state.collect { render() } }
        }
    }
    private fun render() {
        val saved = ConfigRepository.load(requireContext())
        val runtime = MobilecoreRuntime.state.value
        sourceRow?.detail(saved.sourceConfName.ifEmpty { saved.sourceConfPath.substringAfterLast('/') }.ifEmpty { getString(R.string.config_no_source) })
        stateRow?.detail(when {
            saved.sourceNeedsGenerate -> getString(R.string.ux_tg_needs_sync)
            runtime.configApplyPending -> getString(R.string.ux_tg_applying)
            runtime.notice != null -> getString(R.string.ux_tg_config_error)
            runtime.serviceState != "Running" -> getString(R.string.profile_saved)
            else -> runtime.configState
        })
        applyButton?.let {
            it.isEnabled = saved.generatedContent.isNotBlank() && !saved.sourceNeedsGenerate && runtime.serviceState == "Running" && !runtime.configApplyPending
            it.label.setTextColor(requireContext().getColor(R.color.primary))
            it.alpha = if (it.isEnabled) 1f else 0.45f
        }
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
    override fun onSaveInstanceState(outState: Bundle) {
        outState.putStringArrayList("clipboardHandled", ArrayList(clipboardGate.handledIdentities))
        outState.putString("clipboardLast", clipboardGate.lastIdentity)
        super.onSaveInstanceState(outState)
    }

    override fun onDestroyView() {
        view?.viewTreeObserver?.takeIf { it.isAlive }?.removeOnWindowFocusChangeListener(clipboardFocusListener)
        clipboardOffer?.dismiss()
        clipboardOffer = null; sourceRow = null; stateRow = null; applyButton = null; importButton = null
        super.onDestroyView()
    }
}
