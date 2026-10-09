package com.pidal.sakamoto.ui

import android.content.Intent
import android.os.Bundle
import android.provider.Settings
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.AutomaticConnectionSettings
import com.pidal.sakamoto.runtime.ConfigRepository
import org.json.JSONArray
import org.json.JSONObject

class AutomaticConnectionFragment : androidx.fragment.app.Fragment() {
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View = FrameLayout(requireContext()).apply { addView(build().root) }

    private fun refresh() {
        (view as? FrameLayout)?.let { it.removeAllViews(); it.addView(build().root) }
    }

    private fun save(next: AutomaticConnectionSettings) {
        next.save(requireContext())
        refresh()
    }

    private fun build(): GroupedPage {
        val page = GroupedPage(requireContext())
        val settings = AutomaticConnectionSettings.load(requireContext())
        val startup = page.section(getString(R.string.automatic_startup))
        page.toggle(startup, getString(R.string.start_on_boot), settings.startOnBoot).setOnCheckedChangeListener { control, checked ->
            try { save(settings.copy(startOnBoot = checked)) }
            catch (error: Exception) {
                control.isChecked = settings.startOnBoot
                showError(error)
            }
        }
        page.setting(startup, getString(R.string.always_on_vpn), icon = R.drawable.ic_service) {
            startActivity(Intent(Settings.ACTION_VPN_SETTINGS))
        }
        page.note(getString(R.string.automatic_startup_note))
        val routing = page.section(getString(R.string.automatic_proxy_rules))
        val config = ConfigRepository.readGeneratedContent(requireContext())
        val root = config?.let { runCatching { JSONObject(it) }.getOrNull() }
        val outbounds = root?.optJSONArray("outbounds") ?: JSONArray()
        val proxies = (0 until outbounds.length()).map { outbounds.getJSONObject(it) }
            .filter { it.optString("type") !in setOf("direct", "block", "dns") }
            .map { it.optString("tag") }.filter { it.isNotEmpty() }
        page.row(routing, getString(R.string.automatic_proxy_outbound), settings.proxyOutbound.ifEmpty { getString(R.string.automatic_choose_proxy) }) {
            if (proxies.isEmpty()) showError(IllegalArgumentException(getString(R.string.automatic_generate_first)))
            else EditDialogs.choice(requireContext(), getString(R.string.automatic_proxy_outbound), proxies, settings.proxyOutbound) { save(settings.copy(proxyOutbound = it)) }
        }
        page.row(routing, getString(R.string.automatic_domains), getString(R.string.automatic_domain_count, settings.domains.size)) {
            EditDialogs.text(requireContext(), getString(R.string.automatic_domains), settings.domains.joinToString("\n"), multiline = true) {
                save(settings.copy(domains = AutomaticConnectionSettings.normalizeDomains(it)))
            }
        }
        page.row(routing, getString(R.string.automatic_apps), getString(R.string.automatic_app_count, settings.packages.size)) { chooseApps(settings) }
        page.toggle(routing, getString(R.string.automatic_only_apps), settings.onlySelectedApps).setOnCheckedChangeListener { control, checked ->
            try { save(settings.copy(onlySelectedApps = checked)) }
            catch (error: Exception) { control.isChecked = settings.onlySelectedApps; showError(error) }
        }
        page.note(getString(R.string.automatic_routing_note))
        page.note(getString(R.string.automatic_apply_note))
        return page
    }

    private fun chooseApps(settings: AutomaticConnectionSettings) {
        val context = requireContext()
        val intent = Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER)
        val installed = context.packageManager.queryIntentActivities(intent, 0)
            .distinctBy { it.activityInfo.packageName }
            .filter { it.activityInfo.packageName != context.packageName }
            .map { it.activityInfo.packageName to it.loadLabel(context.packageManager).toString() }
            .sortedBy { it.second.lowercase() }
        val missing = settings.packages.filter { pkg -> installed.none { it.first == pkg } }.map { it to it }
        val apps = installed + missing
        val selected = settings.packages.toMutableSet()
        MaterialAlertDialogBuilder(context).setTitle(R.string.automatic_apps)
            .setMultiChoiceItems(apps.map { "${it.second}\n${it.first}" }.toTypedArray(), apps.map { it.first in selected }.toBooleanArray()) { _, index, checked ->
                if (checked) selected.add(apps[index].first) else selected.remove(apps[index].first)
            }
            .setNegativeButton(android.R.string.cancel, null)
            .setPositiveButton(R.string.save) { _, _ ->
                try { save(settings.copy(packages = selected.sorted())) }
                catch (error: Exception) { showError(error) }
            }.show()
    }

    private fun showError(error: Exception) {
        MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.edit_failed)
            .setMessage(error.message ?: getString(R.string.invalid_value)).setPositiveButton(android.R.string.ok, null).show()
    }
}
