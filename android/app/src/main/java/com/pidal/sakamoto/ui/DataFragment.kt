package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.lifecycle.lifecycleScope
import com.pidal.sakamoto.R
import com.pidal.sakamoto.command.CommandClientRuntime
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/** Live traffic page: measurements are rows, explanatory copy stays outside them. */
class DataFragment : androidx.fragment.app.Fragment() {
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext())
        val traffic = page.section(getString(R.string.data_section_traffic))
        val connections = page.row(traffic, getString(R.string.data_connections), "", R.drawable.ic_data)
        val uplink = page.row(traffic, getString(R.string.data_uplink), "", R.drawable.ic_upload)
        val downlink = page.row(traffic, getString(R.string.data_downlink), "", R.drawable.ic_download)
        val groups = page.section(getString(R.string.data_section_routing))
        val groupRow = page.row(groups, getString(R.string.data_active_group), "", R.drawable.ic_node)
        page.note(getString(R.string.data_connections_note, "—"))
        val recent = page.section(getString(R.string.data_recent_connections))
        var lastConnections: List<CommandClientRuntime.ConnectionRow>? = null
        val tailnet = page.section(getString(R.string.data_section_tailscale))
        page.row(tailnet, getString(R.string.data_tailscale_entry), getString(R.string.data_tailscale_note), R.drawable.ic_sync)
        viewLifecycleOwner.lifecycleScope.launch {
            while (isActive) {
                val status = CommandClientRuntime.lastStatus
                if (status == null) {
                    connections.detail(getString(R.string.data_no_status))
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
                    if (rows.isEmpty()) page.row(recent, getString(R.string.data_no_connections))
                    for (row in rows.take(20)) {
                        page.row(recent, row.name, row.outbound + if (row.closed) " · closed" else "", R.drawable.ic_link) {
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
        return page.root
    }

    private fun formatBytes(value: Long): String = when {
        value < 1024 -> getString(R.string.data_bytes, value)
        value < 1024 * 1024 -> getString(R.string.data_kilobytes, value / 1024.0)
        else -> getString(R.string.data_megabytes, value / (1024.0 * 1024.0))
    }
}
