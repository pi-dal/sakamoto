package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.fragment.app.Fragment
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import kotlinx.coroutines.launch

/** Settings index. Long forms live on separate destinations. */
class SettingsFragment : Fragment() {
    private var runtimeRow: GroupedPage.Row? = null

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext())
        val connection = page.section(getString(R.string.settings_section_connections))
        page.row(connection, getString(R.string.tunnel_settings), getString(R.string.tunnel_settings_detail), R.drawable.ic_route) {
            (requireActivity() as MainActivity).openChild(TunnelSettingsFragment(), getString(R.string.tunnel_settings))
        }
        page.row(connection, getString(R.string.vpn_status_title), "", R.drawable.ic_service) {
            (requireActivity() as MainActivity).openChild(VpnStatusFragment(), getString(R.string.vpn_status_title))
        }
        page.row(connection, getString(R.string.settings_tailscale_entry), getString(R.string.settings_tailscale_detail), R.drawable.ic_node) {
            (requireActivity() as MainActivity).openChild(TailscaleFragment(), getString(R.string.settings_tailscale_entry))
        }
        page.row(connection, getString(R.string.settings_s3_entry), getString(R.string.settings_s3_detail), R.drawable.ic_sync) {
            (requireActivity() as MainActivity).openChild(S3SettingsFragment(), getString(R.string.settings_s3_entry))
        }
        page.note(getString(R.string.settings_host_boundary))
        page.row(connection, getString(R.string.experiments_title), getString(R.string.experiments_entry_note), R.drawable.ic_route) {
            (requireActivity() as MainActivity).openChild(ExperimentsFragment(), getString(R.string.experiments_title))
        }
        val app = page.section(getString(R.string.settings_section_app))
        page.row(app, getString(R.string.system_surfaces_title), getString(R.string.system_surfaces_note), R.drawable.ic_info) {
            (requireActivity() as MainActivity).openChild(SystemSurfacesFragment(), getString(R.string.system_surfaces_title))
        }
        page.row(app, getString(R.string.settings_about_entry), getString(R.string.settings_about_detail), R.drawable.ic_info) {
            (requireActivity() as MainActivity).openChild(AboutFragment(), getString(R.string.settings_about_entry))
        }
        runtimeRow = page.row(page.section(getString(R.string.settings_section_runtime)), getString(R.string.settings_runtime_entry), "", R.drawable.ic_service)
        return page.root
    }

    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(Lifecycle.State.STARTED) {
                MobilecoreRuntime.state.collect {
                    runtimeRow?.detail(getString(R.string.settings_runtime, it.phase, it.routingMode.ifEmpty { "—" }, it.configState))
                }
            }
        }
    }

    override fun onDestroyView() {
        runtimeRow = null
        super.onDestroyView()
    }
}
