package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import com.pidal.sakamoto.R
import com.pidal.sakamoto.command.CommandClientRuntime
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/** Live traffic page: measurements are rows, explanatory copy stays outside them. */
class DataFragment : androidx.fragment.app.Fragment() {
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View = android.widget.FrameLayout(requireContext())

    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        val page = GroupedPage(requireContext())
        val traffic = page.section(getString(R.string.data_section_traffic))
        val connections = page.setting(traffic, getString(R.string.data_connections))
        val uplink = page.setting(traffic, getString(R.string.data_uplink))
        val downlink = page.setting(traffic, getString(R.string.data_downlink))
        val groups = page.section(getString(R.string.data_section_routing))
        val groupRow = page.setting(groups, getString(R.string.data_active_group))
        val recent = page.section(getString(R.string.data_recent_connections))
        var lastConnections: List<CommandClientRuntime.ConnectionRow>? = null
        val tailnet = page.section()
        page.setting(tailnet, getString(R.string.data_tailscale_entry), icon = R.drawable.ic_sync) {
            (requireActivity() as com.pidal.sakamoto.MainActivity).openChild(
                TailscaleFragment(),
                getString(R.string.settings_tailscale_entry),
            )
        }
        (view as android.widget.FrameLayout).addView(page.root)
        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(androidx.lifecycle.Lifecycle.State.RESUMED) {
            while (isActive) {
                val status = CommandClientRuntime.lastStatus
                if (status == null) {
                    connections.detail("—")
                    uplink.detail("—"); downlink.detail("—")
                } else {
                    connections.detail(getString(R.string.data_connection_value, status.connectionsIn, status.connectionsOut))
                    uplink.detail(formatBytes(status.uplink))
                    downlink.detail(formatBytes(status.downlink))
                }
                groupRow.detail(CommandClientRuntime.lastGroupsSummary.ifEmpty { "—" })
                val rows = CommandClientRuntime.connections
                if (lastConnections != rows) {
                    recent.removeAllViews()
                    if (rows.isEmpty()) page.setting(recent, getString(R.string.ux_tg_no_connections))
                    for (row in rows.take(20)) {
                        val closed = if (row.closed) " · " + getString(R.string.ux_pages_connection_closed) else ""
                        page.row(recent, row.name, row.outbound + closed, R.drawable.ic_link) {
                            val dialog = com.google.android.material.dialog.MaterialAlertDialogBuilder(requireContext())
                                .setTitle(row.name)
                                .setMessage("${getString(R.string.data_target)}: ${row.destination}\n${getString(R.string.data_source)}: ${row.source}\n${getString(R.string.data_outbound)}: ${row.outbound}\n${row.rule}")
                                .setNegativeButton(android.R.string.cancel, null)
                            if (!row.closed) dialog.setPositiveButton(R.string.data_close_connection) { _, _ ->
                                com.google.android.material.dialog.MaterialAlertDialogBuilder(requireContext())
                                    .setTitle(R.string.data_close_confirm_title).setMessage(R.string.data_close_confirm_message)
                                    .setNegativeButton(android.R.string.cancel, null)
                                    .setPositiveButton(R.string.data_close_connection) { _, _ -> CommandClientRuntime.closeConnection(row.id) }.show()
                            }
                            dialog.show()
                        }
                    }
                    lastConnections = rows
                }
                delay(1000)
            }
            }
        }
    }

    private fun formatBytes(value: Long): String = when {
        value < 1024 -> getString(R.string.data_bytes, value)
        value < 1024 * 1024 -> getString(R.string.data_kilobytes, value / 1024.0)
        else -> getString(R.string.data_megabytes, value / (1024.0 * 1024.0))
    }
}
