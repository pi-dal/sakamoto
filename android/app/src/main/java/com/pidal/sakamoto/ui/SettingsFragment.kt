package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.fragment.app.Fragment
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R

/** Settings index. Long forms live on separate destinations; runtime state stays on Home and VPN status. */
class SettingsFragment : Fragment() {
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext())
        val connection = page.section(getString(R.string.settings_section_connections))
        page.setting(connection, getString(R.string.tunnel_settings), icon = R.drawable.ic_route) {
            (requireActivity() as MainActivity).openChild(TunnelSettingsFragment(), getString(R.string.tunnel_settings))
        }
        page.setting(connection, getString(R.string.settings_tailscale_entry), icon = R.drawable.ic_node) {
            (requireActivity() as MainActivity).openChild(TailscaleFragment(), getString(R.string.settings_tailscale_entry))
        }
        page.setting(connection, getString(R.string.settings_s3_entry), icon = R.drawable.ic_sync) {
            (requireActivity() as MainActivity).openChild(S3SettingsFragment(), getString(R.string.settings_s3_entry))
        }
        val diagnostics = page.section(getString(R.string.ux_pages_settings_section_diagnostics))
        page.setting(diagnostics, getString(R.string.vpn_status_title), icon = R.drawable.ic_service) {
            (requireActivity() as MainActivity).openChild(VpnStatusFragment(), getString(R.string.vpn_status_title))
        }
        page.setting(diagnostics, getString(R.string.experiments_title), icon = R.drawable.ic_route) {
            (requireActivity() as MainActivity).openChild(ExperimentsFragment(), getString(R.string.experiments_title))
        }
        val app = page.section(getString(R.string.settings_section_app))
        page.setting(app, getString(R.string.system_surfaces_title), icon = R.drawable.ic_info) {
            (requireActivity() as MainActivity).openChild(SystemSurfacesFragment(), getString(R.string.system_surfaces_title))
        }
        page.setting(app, getString(R.string.settings_about_entry), icon = R.drawable.ic_info) {
            (requireActivity() as MainActivity).openChild(AboutFragment(), getString(R.string.settings_about_entry))
        }
        return page.root
    }
}
