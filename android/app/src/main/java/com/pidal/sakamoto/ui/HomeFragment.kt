package com.pidal.sakamoto.ui

import android.Manifest
import android.app.Activity
import android.content.pm.PackageManager
import android.net.VpnService
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.view.ViewCompat
import androidx.fragment.app.Fragment
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.pidal.sakamoto.R
import com.pidal.sakamoto.bg.TunnelBoxService
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.databinding.FragmentHomeBinding
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import com.pidal.sakamoto.runtime.RuntimeState
import kotlinx.coroutines.launch
import kotlinx.coroutines.isActive

/** Native grouped settings rows. All phase/reachability semantics stay in Go. */
class HomeFragment : Fragment() {
    private var binding: FragmentHomeBinding? = null
    private var renderingConnection = false
    private var renderedGroups: List<com.pidal.sakamoto.runtime.ConfigRepository.Group>? = null
    private var groupsWereLive = false

    private val vpnPermission =
        registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
            if (result.resultCode == Activity.RESULT_OK) {
                requestNotificationThenStart()
            } else {
                MobilecoreRuntime.setNotice("VPN permission denied")
                render(MobilecoreRuntime.state.value)
            }
        }

    private val notificationPermission =
        registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
            if (!granted) MobilecoreRuntime.setNotice(getString(R.string.notification_required_for_status))
            startTunnel()
        }

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        val b = FragmentHomeBinding.inflate(inflater, container, false)
        binding = b
        b.connectionSwitch.setOnCheckedChangeListener { _, checked ->
            if (!renderingConnection) {
                if (checked) connect() else {
                    MobilecoreRuntime.setServiceState("Stopping")
                    TunnelBoxService.stop(requireContext())
                }
            }
        }
        b.connectionStatusEntry.setOnClickListener {
            (requireActivity() as com.pidal.sakamoto.MainActivity).openChild(VpnStatusFragment(), getString(R.string.vpn_status_title))
        }
        b.modeRow.setOnClickListener { chooseMode() }
        b.selectedValue.setOnClickListener { openProfiles() }
        b.urlTestButton.setOnClickListener {
            CommandClientRuntime.urlTest(MobilecoreRuntime.state.value.selectedNode)
        }
        ViewCompat.setTooltipText(b.connectionStatusEntry, getString(R.string.vpn_status_title))
        if (resources.configuration.layoutDirection == View.LAYOUT_DIRECTION_RTL) b.connectionStatusChevron.scaleX = -1f
        render(MobilecoreRuntime.state.value)
        renderGroups(CommandClientRuntime.groups.value)
        return b.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(Lifecycle.State.STARTED) {
                launch { MobilecoreRuntime.state.collect { render(it); renderGroups(CommandClientRuntime.groups.value) } }
                launch { CommandClientRuntime.groups.collect { renderGroups(it) } }
                launch { while (kotlinx.coroutines.currentCoroutineContext().isActive) { render(MobilecoreRuntime.state.value); kotlinx.coroutines.delay(1000) } }
            }
        }
    }

    override fun onDestroyView() {
        binding = null
        renderedGroups = null
        super.onDestroyView()
    }

    fun requestConnect() {
        if (MobilecoreRuntime.state.value.serviceState in setOf("Running", "Starting")) return
        connect()
    }

    private fun connect() {
        try {
            val intent = VpnService.prepare(requireActivity())
            if (intent != null) {
                // Keep the switch off until consent actually starts the VPN.
                render(MobilecoreRuntime.state.value)
                vpnPermission.launch(intent)
            } else {
                requestNotificationThenStart()
            }
        } catch (error: Exception) {
            MobilecoreRuntime.setNotice("Unable to request VPN permission: ${error.message}")
            render(MobilecoreRuntime.state.value)
        }
    }

    private fun requestNotificationThenStart() {
        if (android.os.Build.VERSION.SDK_INT >= 33 &&
            requireContext().checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        } else startTunnel()
    }

    private fun startTunnel() {
        MobilecoreRuntime.clearNotice()
        MobilecoreRuntime.setServiceState("Starting")
        try {
            TunnelBoxService.start(requireContext())
        } catch (error: Exception) {
            MobilecoreRuntime.setServiceState("Unavailable")
            MobilecoreRuntime.setNotice("Unable to start VPN: ${error.message}")
        }
    }

    private fun chooseMode() {
        val modes = arrayOf("Rule", "Global", "Direct")
        MaterialAlertDialogBuilder(requireContext())
            .setTitle(R.string.choose_routing_mode)
            .setSingleChoiceItems(modes, modes.indexOf(MobilecoreRuntime.state.value.routingMode)) { dialog, index ->
                if (MobilecoreRuntime.state.value.serviceState == "Running") {
                    CommandClientRuntime.setClashMode(modes[index])
                }
                dialog.dismiss()
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun openProfiles(group: String? = null) {
        (requireActivity() as com.pidal.sakamoto.MainActivity).openChild(ProfilesFragment().apply {
            arguments = Bundle().apply { putString("group", group) }
        }, getString(R.string.profiles_title))
    }

    private fun renderGroups(live: List<com.pidal.sakamoto.runtime.ConfigRepository.Group>) {
        val b = binding ?: return
        val running = MobilecoreRuntime.state.value.serviceState == "Running"
        val isLive = live.isNotEmpty() && running
        val groups = if (isLive) live else com.pidal.sakamoto.runtime.ConfigRepository.savedGroups(requireContext())
        if (groups == renderedGroups && isLive == groupsWereLive) return
        renderedGroups = groups; groupsWereLive = isLive
        b.groupsContainer.removeAllViews()
        val page = GroupedPage(requireContext())
        val section = page.section(getString(R.string.profiles_title))
        page.row(section, getString(R.string.profile_open_all), getString(R.string.profile_group_count, groups.size), R.drawable.ic_node) { openProfiles() }
        for (group in groups) {
            page.row(section, group.tag, group.selected.ifEmpty { if (isLive) "—" else getString(R.string.profile_saved) }, R.drawable.ic_route) { openProfiles(group.tag) }
        }
        val content = page.root.getChildAt(0) as android.widget.LinearLayout
        page.root.removeView(content)
        b.groupsContainer.addView(content)
    }

    private fun render(state: RuntimeState) {
        val b = binding ?: return
        val (title, hint) = when (state.phase) {
            "Starting" -> R.string.phase_starting to R.string.phase_hint_starting
            "Stopping" -> R.string.phase_stopping to R.string.phase_hint_stopping
            "Reachable" -> R.string.phase_reachable to R.string.phase_hint_reachable
            "Unverified" -> R.string.phase_unverified to R.string.phase_hint_unverified
            "TUNRunning" -> R.string.phase_tun_running to R.string.phase_hint_tun_running
            "Conflict" -> R.string.phase_conflict to R.string.phase_hint_conflict
            "Unavailable" -> R.string.phase_unavailable to R.string.phase_hint_unavailable
            else -> R.string.phase_disconnected to R.string.phase_hint_disconnected
        }
        b.phaseValue.setText(title)
        val diagnostics = com.pidal.sakamoto.runtime.VpnDiagnostics.snapshot(requireContext())
        b.phaseDescription.text = when {
            state.serviceState == "Running" && !diagnostics.vpn -> getString(R.string.vpn_absent)
            state.serviceState == "Running" && diagnostics.physical.isEmpty() -> getString(R.string.vpn_no_network)
            state.probeState == "Checking" -> getString(R.string.home_network_checking)
            state.probeState == "Unverified" -> state.probeDetail
            else -> getString(hint)
        }
        b.connectionStatusEntry.contentDescription = "${getString(title)}. ${b.phaseDescription.text}. ${getString(R.string.vpn_status_title)}"
        val running = state.serviceState == "Running"
        val busy = state.serviceState == "Starting" || state.serviceState == "Stopping"
        renderingConnection = true
        b.connectionSwitch.isChecked = running || state.serviceState == "Starting"
        b.connectionSwitch.isEnabled = !busy
        renderingConnection = false

        b.modeValue.text = when (state.routingMode) {
            "Rule" -> getString(R.string.mode_rule_detail)
            "Global" -> getString(R.string.mode_global_detail)
            "Direct" -> getString(R.string.mode_direct_detail)
            else -> state.routingMode.ifEmpty { getString(R.string.connect_to_view) }
        }
        b.modeRow.isEnabled = running
        b.modeRow.alpha = if (running) 1f else 0.6f
        b.selectedValue.text = state.selectedNode.ifEmpty { getString(R.string.no_selected_node) }
        b.selectedReachability.text = when {
            state.selectedNode.isEmpty() -> getString(R.string.connect_to_view)
            state.selectedNodeTesting -> getString(R.string.node_latency_testing)
            state.selectedNodeStatus == "Reachable" -> getString(R.string.node_latency_result, state.selectedNodeDelay)
            state.selectedNodeStatus == "Failed" -> getString(R.string.node_latency_failed)
            else -> getString(R.string.node_latency_untested)
        }
        b.urlTestButton.isEnabled = running && state.selectedNode.isNotEmpty() && !state.selectedNodeTesting
        b.routingNote.setText(if (running) R.string.node_testing_hint else R.string.routing_connect_hint)
        b.noticeValue.text = state.notice.orEmpty()
        // Successful probe observations are not errors.
        b.noticePanel.visibility = if (state.notice.isNullOrEmpty()) View.GONE else View.VISIBLE
    }
}
