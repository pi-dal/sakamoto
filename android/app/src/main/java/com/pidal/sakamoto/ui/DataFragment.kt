package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.fragment.app.Fragment
import com.pidal.sakamoto.R
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.databinding.FragmentDataBinding

/**
 * Data page skeleton: live status counters from the libbox command channel
 * (status stream → CommandClientRuntime.lastStatus), plus the built-in
 * Tailscale section boundary.
 *
 * Tailscale on Android is the same built-in endpoint as iOS (the sing-box
 * tailscale endpoint embeds the client in the tunnel process — the AAR is
 * built with with_tailscale). The status/exit-node streams
 * (CommandClient tailscale commands) are a wired-in-iOS, pending-on-Android
 * follow-up; this page says so instead of faking data.
 *
 * STATUS PENDING SDK BUILD VERIFICATION — android/README.md.
 */
class DataFragment : Fragment() {

    private var binding: FragmentDataBinding? = null

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        val b = FragmentDataBinding.inflate(inflater, container, false)
        binding = b
        return b.root
    }

    override fun onResume() {
        super.onResume()
        render()
    }

    override fun onDestroyView() {
        binding = null
        super.onDestroyView()
    }

    private fun render() {
        val b = binding ?: return
        val status = CommandClientRuntime.lastStatus
        b.statusSummary.text = if (status == null) {
            getString(R.string.data_no_status)
        } else {
            getString(
                R.string.data_status,
                status.connectionsIn,
                status.connectionsOut,
                status.uplink,
                status.downlink,
            )
        }
        b.connectionsSummary.text = getString(
            R.string.data_connections_note,
            CommandClientRuntime.lastGroupsSummary.ifEmpty { "—" },
        )
        b.tailscaleSummary.text = getString(R.string.data_tailscale_note)
    }
}
