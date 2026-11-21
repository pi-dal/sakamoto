package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.lifecycle.lifecycleScope
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.snackbar.Snackbar
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.ExperimentRuntime
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.launch

/** Native controls for phone-owned experiments; core evidence limits stay explicit. */
class ExperimentsFragment : androidx.fragment.app.Fragment() {
    private var statusRow: GroupedPage.Row? = null
    private var historySignature: String? = null
    private var renderedProfile: ConfigRepository.StagedConfig? = null
    private var renderedDomains = emptyList<String>()
    private fun statusDetail(message: String) = "$message · ${renderedDomains.size} ${getString(R.string.experiment_learned_short)}"
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View = android.widget.FrameLayout(requireContext()).apply { addView(build().root) }
    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        viewLifecycleOwner.lifecycleScope.launch {
            combine(ExperimentRuntime.status, ConfigRepository.changes) { status, _ -> status }.collect { status ->
                val ctx = requireContext()
                val profile = ConfigRepository.load(ctx)
                if (profile != renderedProfile || ExperimentRuntime.learned(ctx, profile) != renderedDomains ||
                    (!status.pending && ExperimentRuntime.history(ctx, profile).lastOrNull() != historySignature)) refresh()
                statusRow?.detail(statusDetail(status.message))
            }
        }
    }
    private fun refresh() { (view as? android.widget.FrameLayout)?.let { it.removeAllViews(); it.addView(build().root) } }
    private fun build(): GroupedPage {
        val page = GroupedPage(requireContext())
        val expected = ConfigRepository.load(requireContext())
        renderedProfile = expected
        renderedDomains = ExperimentRuntime.learned(requireContext(), expected)
        val settings = ExperimentRuntime.settings(requireContext(), expected)
        fun save(next: ExperimentRuntime.Settings) {
            try { ExperimentRuntime.saveSettings(requireContext(), next, expected) }
            finally { refresh() }
        }
        if (settings.showLegacyFinalGuidance) page.note(getString(R.string.experiment_legacy_final))
        if (settings.needsLearningConfirmation) page.note(getString(R.string.experiment_upgrade_consent))
        val group = page.section(getString(R.string.experiments_title))
        page.row(group, getString(R.string.experiment_mode), settings.mode, R.drawable.ic_route) {
            EditDialogs.choice(requireContext(), getString(R.string.experiment_mode), listOf("off", "on", "auto"), settings.mode) { mode ->
                if (mode == "auto") MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.experiment_auto_confirm).setMessage(R.string.experiment_auto_note)
                    .setPositiveButton(R.string.s3_enable_action) { _, _ -> runCatching { save(settings.copy(mode = mode)) }.onFailure { feedback(it.message.orEmpty()) } }
                    .setNegativeButton(android.R.string.cancel, null).show()
                else runCatching { save(settings.copy(mode = mode)) }.onFailure { feedback(it.message.orEmpty()) }
            }
        }
        page.row(group, getString(R.string.experiment_threshold), settings.threshold.toString(), R.drawable.ic_info) {
            EditDialogs.text(requireContext(), getString(R.string.experiment_threshold), settings.threshold.toString()) { save(settings.copy(threshold = it.toInt())) }
        }
        page.row(group, getString(R.string.experiment_cf), getString(if (settings.cfRegion) R.string.setting_on else R.string.setting_off), R.drawable.ic_info) {
            EditDialogs.choice(requireContext(), getString(R.string.experiment_cf), listOf("off", "on"), if (settings.cfRegion) "on" else "off") { save(settings.copy(cfRegion = it == "on")) }
        }
        page.note(getString(R.string.experiment_signal_limits))
        val fallback = page.section(getString(R.string.proxy_fallback_title))
        if (settings.needsFallbackConfirmation) page.note(getString(R.string.fallback_upgrade_consent))
        page.row(fallback, getString(R.string.experiment_fallback), getString(if (settings.fallback) R.string.setting_on else R.string.setting_off), R.drawable.ic_route) {
            EditDialogs.choice(requireContext(), getString(R.string.experiment_fallback), listOf("off", "on"), if (settings.fallback) "on" else "off") { save(settings.copy(fallback = it == "on")) }
        }
        page.row(fallback, getString(R.string.experiment_recovery_rounds), settings.recoverAfter.toString(), R.drawable.ic_info) {
            EditDialogs.text(requireContext(), getString(R.string.experiment_recovery_rounds), settings.recoverAfter.toString()) { save(settings.copy(recoverAfter = it.toInt())) }
        }
        page.button(fallback, getString(R.string.experiment_recover)) { feedback(ExperimentRuntime.recover()) }
        statusRow = page.row(fallback, getString(R.string.vpn_probe_result), statusDetail(ExperimentRuntime.status.value.message))
        page.note(getString(R.string.experiment_fallback_note))
        val learned = page.section(getString(R.string.experiment_learned))
        renderedDomains.forEach { domain ->
            page.row(learned, domain, "", R.drawable.ic_route) {
                confirmRemove(requireContext(), R.string.remove_policy_title, domain) {
                    runCatching { ExperimentRuntime.removeLearned(requireContext(), domain, expected); refresh() }.onFailure { feedback(it.message.orEmpty()) }
                }
            }
        }
        if (renderedDomains.isEmpty()) page.row(learned, getString(R.string.experiment_no_learned))
        val history = ExperimentRuntime.history(requireContext(), expected)
        historySignature = history.lastOrNull()
        val checks = page.section(getString(R.string.experiment_checks))
        val recentFailures = history.takeLast(20).reversed().mapNotNull { raw ->
            val result = runCatching { org.json.JSONObject(raw) }.getOrNull() ?: return@mapNotNull null
            val domain = result.optString("domain").trim()
            val direct = result.optJSONObject("direct")
            if (domain.isEmpty() || direct?.optBoolean("reachable", true) != false) return@mapNotNull null
            domain to result.optString("detail", getString(R.string.experiment_failure_detail))
        }.distinctBy { it.first }
        if (recentFailures.isNotEmpty()) {
            page.button(checks, getString(R.string.experiment_add_failures), primary = true) {
                val domains = recentFailures.map { it.first }
                val checked = BooleanArray(domains.size)
                MaterialAlertDialogBuilder(requireContext())
                    .setTitle(R.string.experiment_add_failures_title)
                    .setMultiChoiceItems(domains.toTypedArray(), checked) { _, which, isChecked -> checked[which] = isChecked }
                    .setNegativeButton(android.R.string.cancel, null)
                    .setPositiveButton(R.string.add) { _, _ ->
                        val selected = domains.filterIndexed { index, _ -> checked[index] }
                        if (selected.isEmpty()) feedback(getString(R.string.experiment_select_failure_first))
                        else runCatching { ConfigRepository.savePolicyRules(requireContext(), selected, expected); refresh() }
                            .onFailure { feedback(it.message.orEmpty()) }
                    }.show()
            }
        }
        if (history.isEmpty()) page.row(checks, getString(R.string.experiment_no_checks))
        history.takeLast(10).reversed().forEach { raw ->
            val result = org.json.JSONObject(raw)
            val domain = result.getString("domain")
            val detail = result.getString("detail")
            page.row(checks, domain, detail) {
                MaterialAlertDialogBuilder(requireContext()).setTitle(domain).setMessage(detail).setPositiveButton(android.R.string.ok, null).show()
            }
        }
        page.note(getString(R.string.experiment_check_note))
        return page
    }
    private fun feedback(message: String) { view?.let { Snackbar.make(it, message, Snackbar.LENGTH_LONG).show() } }
    override fun onDestroyView() { statusRow = null; super.onDestroyView() }
}
