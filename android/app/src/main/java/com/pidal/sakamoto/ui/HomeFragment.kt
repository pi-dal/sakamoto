package com.pidal.sakamoto.ui

import android.app.Activity
import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.activity.result.contract.ActivityResultContracts
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import com.pidal.sakamoto.bg.TunnelBoxService
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.databinding.FragmentHomeBinding
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import com.pidal.sakamoto.runtime.RuntimeState
import kotlinx.coroutines.launch

/**
 * Home page: the TUI's status bar + service controls, rendered from
 * MobilecoreRuntime (whose every semantic word comes from the Mobilecore
 * AAR — see runtime/MobilecoreRuntime.kt).
 *
 * Connect = VPN permission (VpnService.prepare) then startForegroundService;
 * Disconnect = the SERVICE_CLOSE broadcast the tunnel service listens for.
 * Mode cycle = Mobilecore.nextRoutingMode → CommandClient.setClashMode.
 * URL test  = CommandClient.urlTest on the selected node; the group stream
 * answers with a fresh delay and the bridge words the status.
 */
class HomeFragment : Fragment() {

    private var binding: FragmentHomeBinding? = null

    private val vpnPermission =
        registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
            if (result.resultCode == Activity.RESULT_OK) {
                startTunnel()
            } else {
                MobilecoreRuntime.setNotice("VPN permission denied")
            }
        }

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        val b = FragmentHomeBinding.inflate(inflater, container, false)
        binding = b
        b.connectButton.setOnClickListener { connect() }
        b.disconnectButton.setOnClickListener { TunnelBoxService.stop(requireContext()) }
        b.cycleModeButton.setOnClickListener { cycleMode() }
        b.urlTestButton.setOnClickListener {
            CommandClientRuntime.urlTest(MobilecoreRuntime.state.value.selectedNode)
        }
        return b.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        viewLifecycleOwner.lifecycleScope.launch {
            MobilecoreRuntime.state.collect { state -> render(state) }
        }
    }

    override fun onDestroyView() {
        binding = null
        super.onDestroyView()
    }

    private fun connect() {
        val activity = requireActivity()
        val intent = VpnService.prepare(activity)
        if (intent != null) {
            vpnPermission.launch(intent)
        } else {
            startTunnel()
        }
    }

    private fun startTunnel() {
        MobilecoreRuntime.setServiceState("Starting")
        TunnelBoxService.start(requireContext())
    }

    private fun cycleMode() {
        // The bridge owns the cycle order; libbox applies the word.
        val next = MobilecoreRuntime.cycleRoutingMode()
        CommandClientRuntime.setClashMode(next)
    }

    private fun render(state: RuntimeState) {
        val b = binding ?: return
        b.phaseValue.text = state.phase
        b.serviceStateValue.text =
            getString(com.pidal.sakamoto.R.string.service_state_label) + ": " + state.serviceState
        b.probeStateValue.text =
            getString(com.pidal.sakamoto.R.string.probe_state_label) + ": " + state.probeState
        b.modeValue.text = state.routingMode.ifEmpty { "—" }
        b.selectedValue.text = state.selectedNode.ifEmpty { "—" }
        // selected ≠ reachable is the fact this row exists to show: the dot
        // (selection) is not a connectivity claim.
        b.selectedReachability.text = if (state.selectedNode.isEmpty()) {
            ""
        } else {
            state.selectedNodeStatus + if (state.selectedNodeStatus != "Reachable") {
                "  (selected ≠ reachable)"
            } else {
                ""
            }
        }
        b.configStateValue.text = state.configState
        b.noticeValue.text = state.notice ?: ""
        val running = state.serviceState == "Running" || state.serviceState == "Starting"
        b.connectButton.isEnabled = !running
        b.disconnectButton.isEnabled = running
    }
}
