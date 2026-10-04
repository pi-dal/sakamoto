package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import com.pidal.sakamoto.R
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.databinding.FragmentConfigBinding
import com.pidal.sakamoto.mobilecore.Mobilecore
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject

/**
 * Config tab skeleton with the TUI/iOS section boundaries:
 *   Import config — a Shadowrocket .conf URL is fetched by the app and parsed
 *     through Mobilecore.parseConfContentJSON (the same parser the host
 *     importer runs). The report counts + pending references render here.
 *   Policy — staged rules validated via Mobilecore.normalizePolicyRule; the
 *     state model lives in ConfigRepository.
 *   Nodes & sources — staged share links + subscription METADATA (bodies are
 *     host-fetched; the device never pretends otherwise).
 *   Generate · Apply — Mobilecore.validateConfigJSON (structural, in-process)
 *     then the provider reload (serviceReload). The .srs compilation and
 *     `sing-box check` stay on the sakamoto host — this page says so.
 *
 * STATUS: page skeleton + working import/parse/apply path; the policy/nodes
 * EDITOR forms are follow-ups (the state model is already in
 * ConfigRepository). PENDING SDK BUILD VERIFICATION — android/README.md.
 */
class ConfigFragment : Fragment() {

    private var binding: FragmentConfigBinding? = null

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        val b = FragmentConfigBinding.inflate(inflater, container, false)
        binding = b
        b.importButton.setOnClickListener { importFromUrl() }
        b.applyButton.setOnClickListener { applyToService() }
        return b.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        renderStaged()
    }

    override fun onDestroyView() {
        binding = null
        super.onDestroyView()
    }

    private fun renderStaged() {
        val staged = ConfigRepository.load(requireContext())
        val b = binding ?: return
        b.policySummary.text = if (staged.policy.isEmpty()) {
            getString(R.string.policy_empty)
        } else {
            staged.policy.joinToString("\n") { "${it.match} → ${it.action}" }
        }
        b.nodesSummary.text = buildString {
            appendLine(getString(R.string.nodes_count, staged.nodes.size))
            append(getString(R.string.subscriptions_count, staged.subscriptions.size))
        }
        b.configStateValue.text = MobilecoreRuntime.state.value.configState
    }

    private fun importFromUrl() {
        val b = binding ?: return
        val urlText = b.importUrlInput.text?.toString()?.trim().orEmpty()
        if (urlText.isEmpty()) {
            b.importReport.text = getString(R.string.import_error_empty)
            return
        }
        if (!urlText.startsWith("https://") && !urlText.startsWith("http://")) {
            // Local .conf paste/file is a follow-up; keep the boundary honest.
            b.importReport.text = getString(R.string.import_error_scheme)
            return
        }
        b.importButton.isEnabled = false
        viewLifecycleOwner.lifecycleScope.launch {
            val result = withContext(Dispatchers.IO) {
                runCatching {
                    val connection = java.net.URI(urlText).toURL().openConnection() as java.net.HttpURLConnection
                    connection.connectTimeout = 10_000
                    connection.readTimeout = 20_000
                    connection.inputStream.bufferedReader().use { it.readText() }
                }
            }
            b.importButton.isEnabled = true
            result.fold(
                onSuccess = { content ->
                    // The same parser the host importer uses (single JSON
                    // string across the gomobile boundary).
                    val report = try {
                        Mobilecore.parseConfContentJSON(content)
                    } catch (e: Exception) {
                        b.importReport.text = getString(R.string.import_error_parse, e.message)
                        return@fold
                    }
                    val json = JSONObject(report)
                    val summary = getString(
                        R.string.import_summary,
                        json.optInt("totalRules"),
                        json.optInt("proxyRules"),
                        json.optInt("directRules"),
                        json.optInt("rejectRules"),
                    )
                    b.importReport.text = summary
                    val staged = ConfigRepository.load(requireContext())
                    ConfigRepository.save(
                        requireContext(),
                        staged.copy(
                            sourceConfName = urlText.substringAfterLast('/'),
                            sourceConfIsUrl = true,
                            sourceConfContent = content,
                        ),
                    )
                    MobilecoreRuntime.configEvent("modified")
                    renderStaged()
                },
                onFailure = { e ->
                    b.importReport.text = getString(R.string.import_error_fetch, e.message)
                },
            )
        }
    }

    private fun applyToService() {
        val context = requireContext()
        val content = ConfigRepository.readGeneratedContent(context)
        if (content == null) {
            MobilecoreRuntime.setNotice(getString(R.string.apply_error_no_config))
            return
        }
        // Structural check in-process (the bridge throws on invalid JSON).
        try {
            Mobilecore.validateConfigJSON(content)
        } catch (e: Exception) {
            MobilecoreRuntime.configEvent("regenerate_failed")
            MobilecoreRuntime.setNotice(getString(R.string.apply_error_check, e.message))
            return
        }
        MobilecoreRuntime.configEvent("regenerate_succeeded")
        // iOS collapses Regenerate + Reconnect into a provider reload; the
        // Android equivalent is the CommandClient reload of the running box.
        CommandClientRuntime.start()
        CommandClientRuntime.reloadService()
        MobilecoreRuntime.configEvent("applied")
        MobilecoreRuntime.clearNotice()
        renderStaged()
    }
}
