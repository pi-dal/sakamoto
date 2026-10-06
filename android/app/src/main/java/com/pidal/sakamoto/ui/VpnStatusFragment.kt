package com.pidal.sakamoto.ui

import android.content.Intent
import android.os.Bundle
import android.provider.Settings
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import com.pidal.sakamoto.runtime.VpnDiagnostics
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.text.DateFormat
import java.util.Date

/** Device observations and explicit end-to-end test, not a boolean 'Running'. */
class VpnStatusFragment : androidx.fragment.app.Fragment() {
    private var update: (() -> Unit)? = null

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val context = requireContext()
        val page = GroupedPage(context)
        val system = page.section(getString(R.string.vpn_system_section))
        val consent = page.row(system, getString(R.string.vpn_consent))
        val tun = page.row(system, getString(R.string.vpn_interface))
        val physical = page.row(system, getString(R.string.vpn_underlying))
        val dns = page.row(system, getString(R.string.vpn_dns))
        val core = page.section(getString(R.string.vpn_core_section))
        val service = page.row(core, getString(R.string.service_state_label))
        val channel = page.row(core, getString(R.string.vpn_channel))
        val probe = page.section(getString(R.string.vpn_probe_section))
        val result = page.row(probe, getString(R.string.vpn_probe_result))
        val timestamp = page.row(probe, getString(R.string.vpn_probe_time))
        val check = page.button(probe, getString(R.string.vpn_check_now), primary = true) {
            viewLifecycleOwner.lifecycleScope.launch { VpnDiagnostics.probe(context) }
        }
        page.note(getString(R.string.vpn_probe_note))
        page.button(system, getString(R.string.vpn_open_settings)) { startActivity(Intent(Settings.ACTION_VPN_SETTINGS)) }
        page.button(system, getString(R.string.vpn_open_network)) { startActivity(Intent(Settings.ACTION_WIRELESS_SETTINGS)) }
        val error = page.row(core, getString(R.string.vpn_last_error))
        update = {
            val status = VpnDiagnostics.snapshot(context)
            val runtime = MobilecoreRuntime.state.value
            consent.detail(getString(if (status.consent) R.string.vpn_authorized else R.string.vpn_needs_consent))
            tun.detail(if (status.vpn) status.interfaceName.ifEmpty { getString(R.string.vpn_present) } else getString(R.string.vpn_absent))
            physical.detail(status.physical.ifEmpty { getString(R.string.vpn_no_network) })
            dns.detail(status.dns.ifEmpty { "—" })
            service.detail(runtime.serviceState)
            channel.detail(getString(if (status.channel) R.string.vpn_channel_up else R.string.vpn_channel_down))
            result.detail(runtime.probeDetail)
            timestamp.detail(if (runtime.probeAt == 0L) getString(R.string.vpn_not_checked) else DateFormat.getDateTimeInstance().format(Date(runtime.probeAt)))
            error.detail(runtime.notice ?: getString(R.string.vpn_no_error))
            check.isEnabled = runtime.serviceState == "Running" && runtime.probeState != "Checking" && status.vpn
        }
        update?.invoke()
        return page.root
    }

    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(Lifecycle.State.RESUMED) {
                while (isActive) { update?.invoke(); delay(1000) }
            }
        }
    }
    override fun onDestroyView() { update = null; super.onDestroyView() }
}
