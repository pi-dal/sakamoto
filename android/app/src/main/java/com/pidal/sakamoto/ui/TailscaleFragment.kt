package com.pidal.sakamoto.ui

import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.text.InputType
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.pidal.sakamoto.R
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.runtime.PeerTCPDiagnostics
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import com.pidal.sakamoto.BuildConfig
import android.widget.TextView
import androidx.core.widget.doAfterTextChanged
import com.google.android.material.button.MaterialButton
import kotlinx.coroutines.Job
import com.pidal.sakamoto.runtime.TailscaleCapabilities
import com.pidal.sakamoto.runtime.TailscaleRuntime
import com.pidal.sakamoto.runtime.TailscaleUiState
import com.pidal.sakamoto.security.TailscaleAuthKeyStore
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.launch

/** Tailscale detail page using the same grouped list grammar as Settings. */
class TailscaleFragment : androidx.fragment.app.Fragment() {
    private var status: GroupedPage.Row? = null
    private var tailnet: GroupedPage.Row? = null
    private var exit: GroupedPage.Row? = null
    private var auth: GroupedPage.Row? = null
    private var peers: GroupedPage.Row? = null
    private var notice: GroupedPage.Row? = null
    private var diagnosticTargetRow: GroupedPage.Row? = null
    private var diagnosticPort: EditText? = null
    private var diagnosticResult: TextView? = null
    private var pingHistory: TextView? = null
    private var checkButton: MaterialButton? = null
    private var shareButton: MaterialButton? = null
    private var diagnosticJob: Job? = null
    private var diagnosticGeneration = 0L
    private var diagnosticReport = ""
    private var verifySSH = true
    private var diagnosticTarget: DiagnosticTarget? = null

    private data class DiagnosticTarget(val endpoint: String, val peerID: String, val name: String, val address: String) {
        val label: String get() = "$name · $address"
    }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext())
        val overview = page.section(getString(R.string.ts_section_status))
        status = page.row(overview, getString(R.string.ts_backend_status), getString(R.string.ts_not_subscribed), R.drawable.ic_node)
        tailnet = page.row(overview, getString(R.string.ts_tailnet_entry), "", R.drawable.ic_sync)
        val authentication = page.section(getString(R.string.ts_section_auth))
        auth = page.row(authentication, getString(R.string.ts_authkey_title), "", R.drawable.ic_info) { promptStoreKey() }
        page.button(authentication, getString(R.string.ts_open_auth_url)) { openAuthURL() }
        page.button(authentication, getString(R.string.ts_authkey_delete)) {
            MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.ts_authkey_delete_confirm_title).setMessage(R.string.ts_authkey_delete_confirm_message).setNegativeButton(android.R.string.cancel, null).setPositiveButton(R.string.ts_authkey_delete) { _, _ -> TailscaleAuthKeyStore(requireContext()).deleteAuthKey(); refreshAuthKeyState() }.show()
        }
        page.note(getString(R.string.ts_authkey_note))
        val exitGroup = page.section(getString(R.string.ts_section_exit))
        exit = page.row(exitGroup, getString(R.string.ts_exit_node_entry), getString(R.string.ts_exit_none), R.drawable.ic_route) { chooseExitNode() }
        page.button(exitGroup, getString(R.string.ts_clear_exit)) {
            MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.ts_clear_exit_confirm_title).setMessage(R.string.ts_clear_exit_confirm_message).setNegativeButton(android.R.string.cancel, null).setPositiveButton(R.string.ts_clear_exit) { _, _ -> clearExitNode() }.show()
        }
        page.button(exitGroup, getString(R.string.ts_ping_exit)) { pingExitNode() }
        val peerGroup = page.section(getString(R.string.ts_section_peers))
        peers = page.row(peerGroup, getString(R.string.ts_peers), "", R.drawable.ic_node)
        val diagnostics = page.section(getString(R.string.ts_diagnostics_title))
        diagnosticTargetRow = page.row(diagnostics, getString(R.string.ts_diagnostic_target), getString(R.string.ts_choose_peer), R.drawable.ic_node) { chooseDiagnosticPeer() }
        diagnosticPort = page.field(diagnostics, getString(R.string.ts_tcp_port), "22").apply {
            inputType = InputType.TYPE_CLASS_NUMBER
            doAfterTextChanged { invalidateDiagnostic() }
        }
        page.toggle(diagnostics, getString(R.string.ts_check_ssh_greeting), verifySSH).setOnCheckedChangeListener { _, checked ->
            verifySSH = checked
            invalidateDiagnostic()
        }
        page.button(diagnostics, getString(R.string.ts_ping_target)) {
            diagnosticTarget?.let { CommandClientRuntime.startTailscalePing(it.endpoint, it.address) }
                ?: TailscaleRuntime.setNotice(getString(R.string.ts_choose_peer))
        }
        checkButton = page.button(diagnostics, getString(R.string.ts_check_tcp)) { startDiagnostic() }
        page.button(diagnostics, getString(R.string.ts_cancel_check)) { invalidateDiagnostic() }
        diagnosticResult = page.paragraph(diagnostics, getString(R.string.ts_diagnostics_idle))
        shareButton = page.button(diagnostics, getString(R.string.ts_share_diagnostic)) {
            if (diagnosticReport.isNotEmpty()) {
                startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).apply {
                    type = "text/plain"
                    putExtra(Intent.EXTRA_TEXT, diagnosticReport)
                }, getString(R.string.ts_share_diagnostic)))
            }
        }.apply { isEnabled = false }
        pingHistory = page.paragraph(diagnostics, "")
        page.note(getString(R.string.ts_diagnostics_note))
        val advanced = page.section(getString(R.string.ts_section_advanced))
        page.row(advanced, getString(R.string.ts_logout), getString(R.string.ts_logout_confirm), R.drawable.ic_info) { confirmLogout() }
        notice = page.row(advanced, getString(R.string.ts_notice_title), "", R.drawable.ic_info)
        page.note(TailscaleCapabilities.unsupported.joinToString("\n") { "${it.capability}: ${it.reason}" })
        return page.root
    }

    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        refreshAuthKeyState()
        CommandClientRuntime.start(); CommandClientRuntime.subscribeTailscaleStatus()
        viewLifecycleOwner.lifecycleScope.launch { TailscaleRuntime.state.collect { render(it) } }
        viewLifecycleOwner.lifecycleScope.launch { CommandClientRuntime.channelConnected.collect { if (!it) invalidateDiagnostic(); updateDiagnosticButtons() } }
        viewLifecycleOwner.lifecycleScope.launch { MobilecoreRuntime.state.collect { if (it.serviceState != "Running") invalidateDiagnostic(); updateDiagnosticButtons() } }
    }

    private fun refreshAuthKeyState() {
        val key = TailscaleAuthKeyStore(requireContext()).readAuthKey()
        auth?.detail(if (key == null) getString(R.string.ts_authkey_absent) else getString(R.string.ts_authkey_stored, com.pidal.sakamoto.runtime.TailscaleAuthKeyMasking.mask(key)))
    }

    private fun render(state: TailscaleUiState) {
        val primary = state.primary
        status?.detail(primary?.backendState?.wireString ?: getString(R.string.ts_not_subscribed))
        tailnet?.detail(primary?.let { getString(R.string.ts_tailnet_line, it.networkName.ifEmpty { "—" }, it.magicDNSSuffix.ifEmpty { "—" }) } ?: "")
        exit?.detail(primary?.exitNodePeer?.let { getString(R.string.ts_exit_line, it.displayName) } ?: getString(R.string.ts_exit_none))
        peers?.detail(primary?.peers?.joinToString(" · ") { it.displayName + if (it.online) " · online" else "" } ?: "—")
        notice?.detail(state.notice.orEmpty())
        pingHistory?.text = state.pingLines.take(3).joinToString("\n")
        if (diagnosticTarget != null && diagnosticTarget !in diagnosticTargets()) {
            diagnosticTarget = null
            diagnosticTargetRow?.detail(getString(R.string.ts_choose_peer))
            invalidateDiagnostic()
        }
        updateDiagnosticButtons()
    }

    private fun openAuthURL() { TailscaleRuntime.state.value.authURL.takeIf { it.isNotEmpty() }?.let { try { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(it))) } catch (_: ActivityNotFoundException) { TailscaleRuntime.setNotice(getString(R.string.ts_no_browser)) } } }
    private fun promptStoreKey() {
        EditDialogs.text(requireContext(), getString(R.string.ts_authkey_store), "", secret = true) { value ->
            require(value.trim().isNotEmpty()) { getString(R.string.required_value) }
            TailscaleAuthKeyStore(requireContext()).storeAuthKey(value.trim())
            refreshAuthKeyState()
        }
    }

    override fun onDestroyView() {
        CommandClientRuntime.cancelTailscalePing()
        diagnosticJob?.cancel()
        diagnosticGeneration += 1
        diagnosticJob = null; diagnosticTarget = null; diagnosticReport = ""
        diagnosticTargetRow = null; diagnosticPort = null; diagnosticResult = null
        pingHistory = null; checkButton = null; shareButton = null
        status = null; tailnet = null; exit = null; auth = null; peers = null; notice = null
        super.onDestroyView()
    }
    private fun diagnosticTargets(): List<DiagnosticTarget> = TailscaleRuntime.state.value.endpoints.flatMap { endpoint ->
        endpoint.peers.flatMap { peer -> peer.tailscaleIPs.map { DiagnosticTarget(endpoint.endpointTag, peer.stableID, peer.displayName, it) } }
    }

    private fun chooseDiagnosticPeer() {
        val targets = diagnosticTargets()
        if (targets.isEmpty()) { TailscaleRuntime.setNotice(getString(R.string.ts_not_subscribed)); return }
        MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.ts_choose_peer)
            .setItems(targets.map { it.label }.toTypedArray()) { _, which ->
                invalidateDiagnostic()
                diagnosticTarget = targets[which]
                diagnosticTargetRow?.detail(targets[which].label)
                updateDiagnosticButtons()
            }.setNegativeButton(android.R.string.cancel, null).show()
    }

    private fun invalidateDiagnostic() {
        diagnosticGeneration += 1
        diagnosticJob?.cancel()
        diagnosticJob = null
        diagnosticReport = ""
        diagnosticResult?.text = getString(R.string.ts_diagnostics_idle)
        updateDiagnosticButtons()
    }

    private fun updateDiagnosticButtons() {
        val port = diagnosticPort?.text?.toString()?.toIntOrNull()
        checkButton?.isEnabled = diagnosticJob == null && diagnosticTarget != null && port != null && port in 1..65535 &&
            CommandClientRuntime.channelConnected.value && MobilecoreRuntime.state.value.serviceState == "Running"
        shareButton?.isEnabled = diagnosticReport.isNotEmpty()
        checkButton?.setText(if (diagnosticJob == null) R.string.ts_check_tcp else R.string.ts_checking_tcp)
    }

    private fun startDiagnostic() {
        val target = diagnosticTarget ?: return
        val port = diagnosticPort?.text?.toString()?.toIntOrNull()?.takeIf { it in 1..65535 } ?: return
        if (target !in diagnosticTargets() || diagnosticJob != null) return
        invalidateDiagnostic()
        val generation = diagnosticGeneration
        val appContext = requireContext().applicationContext
        diagnosticResult?.text = getString(R.string.ts_checking_tcp)
        val checkSSH = verifySSH
        diagnosticJob = viewLifecycleOwner.lifecycleScope.launch(start = kotlinx.coroutines.CoroutineStart.LAZY) {
            try {
                val result = PeerTCPDiagnostics.check(appContext, target.address, port, verifySSH = checkSSH)
                if (generation != diagnosticGeneration || target !in diagnosticTargets()) return@launch
                diagnosticReport = "sakamoto Android ${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})\nOS: Android ${android.os.Build.VERSION.RELEASE} (API ${android.os.Build.VERSION.SDK_INT})\nEndpoint: ${target.endpoint}\nPeer: ${target.name}\nBound network: Sakamoto VPN\n" + result.report
                diagnosticResult?.text = diagnosticReport
            } finally {
                if (generation == diagnosticGeneration) { diagnosticJob = null; updateDiagnosticButtons() }
            }
        }
        diagnosticJob?.start()
        updateDiagnosticButtons()
    }

    private fun chooseExitNode() {
        val primary = TailscaleRuntime.state.value.primary ?: run {
            TailscaleRuntime.setNotice(getString(R.string.ts_not_subscribed)); return
        }
        val candidates = primary.exitNodeCandidates
        if (candidates.isEmpty()) { TailscaleRuntime.setNotice(getString(R.string.ts_no_candidates)); return }
        MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.ts_choose_exit_node).setItems(candidates.map { it.displayName }.toTypedArray()) { _, which -> CommandClientRuntime.setTailscaleExitNode(primary.endpointTag, candidates[which].stableID) }.setNegativeButton(android.R.string.cancel, null).show()
    }

    private fun clearExitNode() {
        val primary = TailscaleRuntime.state.value.primary ?: run { TailscaleRuntime.setNotice(getString(R.string.ts_not_subscribed)); return }
        CommandClientRuntime.setTailscaleExitNode(primary.endpointTag, "")
    }
    private fun pingExitNode() {
        val primary = TailscaleRuntime.state.value.primary
        val target = primary?.exitNodePingTarget
        if (primary == null || target == null) { TailscaleRuntime.setNotice(getString(R.string.ts_no_ping_target)); return }
        CommandClientRuntime.startTailscalePing(primary.endpointTag, target)
    }
    private fun confirmLogout() {
        val primary = TailscaleRuntime.state.value.primary ?: run { TailscaleRuntime.setNotice(getString(R.string.ts_not_subscribed)); return }
        MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.ts_logout).setMessage(R.string.ts_logout_confirm)
            .setPositiveButton(R.string.ts_logout) { _, _ -> CommandClientRuntime.tailscaleLogout(primary.endpointTag) }
            .setNegativeButton(android.R.string.cancel, null).show()
    }
}
