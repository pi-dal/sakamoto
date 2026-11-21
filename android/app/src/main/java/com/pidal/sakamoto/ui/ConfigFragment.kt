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
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject

/** Index of real editing workflows with an explicit, acknowledged apply action. */
class ConfigFragment(private val sourceClient: okhttp3.OkHttpClient = okhttp3.OkHttpClient.Builder()
    .followRedirects(false).followSslRedirects(false).readTimeout(20, java.util.concurrent.TimeUnit.SECONDS).build()) : androidx.fragment.app.Fragment() {
    private var sourceRow: GroupedPage.SettingRow? = null
    private var stateRow: GroupedPage.SettingRow? = null
    private var applyButton: GroupedPage.SettingRow? = null
    internal var importing = false
        private set
    private var importButton: GroupedPage.SettingRow? = null
    private val clipboardGate = ClipboardImportGate()
    private var clipboardOffer: Snackbar? = null
    private val clipboardFocusListener = android.view.ViewTreeObserver.OnWindowFocusChangeListener { focused ->
        if (focused && lifecycle.currentState.isAtLeast(androidx.lifecycle.Lifecycle.State.RESUMED)) probeClipboard()
    }
    private var scanReceipt: String? = null
    private var pickerReceipt: String? = null
    private fun receipt() = ConfigRepository.manifestReceipt(requireContext())
    private val qrScanner = registerForActivityResult(com.journeyapps.barcodescanner.ScanContract()) { result ->
        val origin = scanReceipt
        scanReceipt = null
        if (result.originalIntent?.getBooleanExtra(com.google.zxing.client.android.Intents.Scan.MISSING_CAMERA_PERMISSION, false) == true) importFeedback(R.string.ux_import_camera_denied)
        else result.contents?.let { routeImport(it, origin) }
    }
    private val packagePicker = registerForActivityResult(androidx.activity.result.contract.ActivityResultContracts.OpenDocument()) { uri ->
        val origin = pickerReceipt
        pickerReceipt = null
        if (uri != null) viewLifecycleOwner.lifecycleScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    val context = requireContext().applicationContext
                    val expected = ConfigRepository.loadAt(context, origin) ?: error(getString(R.string.editor_conflict))
                    context.contentResolver.openInputStream(uri)?.use { input ->
                        com.pidal.sakamoto.runtime.TunnelPackage.import(context, input, expected) { ensureActive() }
                    }
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
            val expected = ConfigRepository.load(requireContext())
            EditDialogs.text(requireContext(), getString(R.string.config_import_hint), "") { url ->
                val input = url.trim()
                require(input.startsWith("https://") || input.startsWith("http://")) { getString(R.string.import_error_scheme) }
                Mobilecore.validateSourceURL(input)
                importFromUrl(input, expected)
            }
        }
        page.setting(source, getString(R.string.ux_import_clipboard), icon = R.drawable.ic_download) {
            val origin = receipt()
            val clipboard = requireContext().getSystemService(android.content.ClipboardManager::class.java)
            val text = clipboard.primaryClip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.text?.toString()
            if (text == null) importFeedback(R.string.ux_import_invalid) else routeImport(text, origin)
        }
        page.setting(source, getString(R.string.ux_import_qr), icon = R.drawable.ic_node) {
            scanReceipt = receipt()
            qrScanner.launch(com.journeyapps.barcodescanner.ScanOptions().apply {
                setDesiredBarcodeFormats(com.journeyapps.barcodescanner.ScanOptions.QR_CODE)
                setPrompt(getString(R.string.ux_import_scan_prompt))
                setBeepEnabled(false)
                setOrientationLocked(false)
                setBarcodeImageEnabled(false)
            })
        }
        page.setting(source, getString(R.string.tunnel_package_import), icon = R.drawable.ic_download) {
            pickerReceipt = receipt()
            packagePicker.launch(arrayOf("application/zip", "application/octet-stream"))
        }
        val manage = page.section(getString(R.string.config_manage_section))
        page.setting(manage, getString(R.string.proxy_groups_title), icon = R.drawable.ic_route) { open(ProxyGroupsFragment(), R.string.proxy_groups_title) }
        page.setting(manage, getString(R.string.profiles_title), icon = R.drawable.ic_node) { open(ProfilesFragment(), R.string.profiles_title) }
        page.setting(manage, getString(R.string.policy_effective_rules), icon = R.drawable.ic_route) { open(RoutingPolicyFragment(), R.string.policy_effective_rules) }
        page.setting(manage, getString(R.string.regional_dns_title), icon = R.drawable.ic_route) { open(RegionalDNSFragment(), R.string.regional_dns_title) }
        page.setting(manage, getString(R.string.proxy_chain_title), icon = R.drawable.ic_route) { open(ProxyChainFragment(), R.string.proxy_chain_title) }
        page.setting(manage, getString(R.string.experiments_title), icon = R.drawable.ic_route) { open(ExperimentsFragment(), R.string.experiments_title) }
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
            val expected = ConfigRepository.load(requireContext())
            MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.config_apply_confirm_title).setMessage(R.string.config_apply_confirm_message)
                .setNegativeButton(android.R.string.cancel, null).setPositiveButton(R.string.apply_saved) { _, _ -> apply(expected) }.show()
        }
        page.setting(apply, getString(R.string.edit_generated_config)) {
            val expected = ConfigRepository.load(requireContext())
            EditDialogs.text(requireContext(), getString(R.string.edit_generated_config), expected.generatedContent, multiline = true) {
                ConfigRepository.saveGeneratedEdit(requireContext(), it, expected = expected); render()
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
        val origin = receipt()
        val clipboard = context.getSystemService(android.content.ClipboardManager::class.java) ?: return
        val text = clipboard.primaryClip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.text?.toString() ?: return
        val candidate = try { ImportPayload.detect(text) } catch (_: Exception) { return }
        val identity = clipboardIdentity(candidate.text)
        if (clipboardGate.decide(identity) != ClipboardImportGate.Decision.OFFER) return
        showClipboardOffer(candidate, identity, origin)
    }

    /** Stable content key for dedup — clipboard text never persists in memory beyond the route. */
    private fun clipboardIdentity(text: String): String =
        java.security.MessageDigest.getInstance("SHA-256")
            .digest(text.toByteArray(Charsets.UTF_8)).joinToString("") { "%02x".format(it) }

    private fun showClipboardOffer(candidate: ImportPayload.Candidate, identity: String, origin: String) {
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
                routeImport(candidate.text, origin)
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

    private fun routeImport(raw: String, origin: String?) {
        try {
            val expected = ConfigRepository.loadAt(requireContext(), origin) ?: return importFeedback(R.string.editor_conflict)
            val candidate = ImportPayload.detect(raw)
            when (candidate.kind) {
                ImportPayload.Kind.NODE -> {
                    Mobilecore.parseShareLink(candidate.text)
                    AddNodeSheet().apply { arguments = Bundle().apply { putString("prefill", candidate.text); putString("origin", origin) } }
                        .show(parentFragmentManager, "import-node")
                }
                ImportPayload.Kind.URL -> MaterialAlertDialogBuilder(requireContext())
                    .setTitle(R.string.ux_import_url_title)
                    .setItems(arrayOf(getString(R.string.ux_import_url_config), getString(R.string.ux_import_url_subscription))) { _, index ->
                        if (ConfigRepository.load(requireContext()) != expected) importFeedback(R.string.editor_conflict)
                        else if (index == 0) importFromUrl(candidate.text, expected)
                        else AddSubscriptionSheet().apply {
                            arguments = Bundle().apply { putString("prefill", candidate.text); putString("origin", origin) }
                        }.show(parentFragmentManager, "import-subscription")
                    }.setNegativeButton(android.R.string.cancel, null).show()
                ImportPayload.Kind.CONFIG_JSON, ImportPayload.Kind.CONF -> {
                    val current = expected
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
                                if (candidate.kind == ImportPayload.Kind.CONFIG_JSON) ConfigRepository.saveGeneratedEdit(requireContext(), candidate.text, expected = current)
                                else {
                                    ConfigRepository.save(requireContext(), current.copy(sourceConfContent = candidate.text,
                                        sourceConfName = getString(R.string.ux_import_source_name), sourceConfIsUrl = false,
                                        sourceConfPath = "", hostSnapshotAt = "", sourceNeedsGenerate = true), current)
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
        if (state != null) {
            scanReceipt = state.getString("scanReceipt")
            pickerReceipt = state.getString("pickerReceipt")
        }
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
    private fun apply(expected: ConfigRepository.StagedConfig) {
        if (ConfigRepository.load(requireContext()) != expected) return importFeedback(R.string.editor_conflict)
        MobilecoreRuntime.beginConfigApply()
        render()
        viewLifecycleOwner.lifecycleScope.launch {
            val ok = withContext(Dispatchers.IO) { CommandClientRuntime.reloadService(expected) }
            if (!ok) MobilecoreRuntime.configEvent("regenerate_failed")
            render()
        }
    }
    internal fun importFromUrl(url: String, expected: ConfigRepository.StagedConfig) {
        if (importing) return
        val context = requireContext().applicationContext
        check(ConfigRepository.load(context) == expected) { getString(R.string.editor_conflict) }
        importing = true; importButton?.isEnabled = false
        viewLifecycleOwner.lifecycleScope.launch {
            try {
                val content = withContext(Dispatchers.IO) {
                    val request = okhttp3.Request.Builder().url(url).build()
                    val host = request.url.host.removeSuffix(".")
                    require(request.url.isHttps && request.url.username.isEmpty() && request.url.password.isEmpty()) { "Remote sources require HTTPS" }
                    require(expected.regionalDNS["domains"].orEmpty().split(',').none { it.trim().removeSuffix(".").equals(host, true) }) { "Protected source host" }
                    check(ConfigRepository.load(context) == expected) { getString(R.string.editor_conflict) }
                    sourceClient.newCall(request).execute().use { response ->
                        check(response.code == 200) { "HTTP ${response.code}" }
                        val body = requireNotNull(response.body).source()
                        require(!body.request((16L shl 20) + 1)) { "Import exceeds 16 MiB" }
                        body.readUtf8()
                    }
                }
                Mobilecore.parseConfContentJSON(content)
                ConfigRepository.save(context, expected.copy(sourceConfName = java.net.URI(url).path.substringAfterLast('/'), sourceConfIsUrl = true, sourceConfContent = content, sourceConfPath = "", hostSnapshotAt = "", sourceNeedsGenerate = true), expected)
                MobilecoreRuntime.configEvent("modified")
                view?.let { Snackbar.make(it, R.string.profile_source_imported, Snackbar.LENGTH_LONG).show() }
            } catch (cancelled: kotlinx.coroutines.CancellationException) { throw cancelled }
            catch (_: Exception) { view?.let { Snackbar.make(it, R.string.profile_import_failed, Snackbar.LENGTH_LONG).show() } }
            finally { importing = false; importButton?.isEnabled = true; if (isAdded) render() }
        }
    }
    override fun onSaveInstanceState(outState: Bundle) {
        outState.putString("scanReceipt", scanReceipt)
        outState.putString("pickerReceipt", pickerReceipt)
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
