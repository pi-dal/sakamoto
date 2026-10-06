package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.lifecycle.lifecycleScope
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.snackbar.Snackbar
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.ExperimentRuntime
import kotlinx.coroutines.launch

/** Native controls for phone-owned experiments; core evidence limits stay explicit. */
class ExperimentsFragment : androidx.fragment.app.Fragment() {
    private var statusRow: GroupedPage.Row? = null
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View = android.widget.FrameLayout(requireContext()).apply { addView(build().root) }
    override fun onViewCreated(view: View, state: Bundle?) {
        super.onViewCreated(view, state)
        viewLifecycleOwner.lifecycleScope.launch { ExperimentRuntime.status.collect { statusRow?.detail("${it.message} · ${it.learned} learned") } }
    }
    private fun refresh() { (view as? android.widget.FrameLayout)?.let { it.removeAllViews(); it.addView(build().root) } }
    private fun build(): GroupedPage {
        val page = GroupedPage(requireContext())
        val settings = ExperimentRuntime.settings(requireContext())
        fun save(next: ExperimentRuntime.Settings) { ExperimentRuntime.saveSettings(requireContext(), next); refresh() }
        val group = page.section(getString(R.string.experiments_title))
        page.row(group, getString(R.string.experiment_mode), settings.mode, R.drawable.ic_route) {
            EditDialogs.choice(requireContext(), getString(R.string.experiment_mode), listOf("off", "on", "auto"), settings.mode) { mode ->
                if (mode == "auto") MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.experiment_auto_confirm).setMessage(R.string.experiment_auto_note)
                    .setPositiveButton(R.string.s3_enable_action) { _, _ -> runCatching { save(settings.copy(mode = mode)) }.onFailure { feedback(it.message.orEmpty()) } }
                    .setNegativeButton(android.R.string.cancel, null).show()
                else save(settings.copy(mode = mode))
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
        page.row(fallback, getString(R.string.experiment_fallback), getString(if (settings.fallback) R.string.setting_on else R.string.setting_off), R.drawable.ic_route) {
            EditDialogs.choice(requireContext(), getString(R.string.experiment_fallback), listOf("off", "on"), if (settings.fallback) "on" else "off") { save(settings.copy(fallback = it == "on")) }
        }
        page.row(fallback, getString(R.string.experiment_recovery_rounds), settings.recoverAfter.toString(), R.drawable.ic_info) {
            EditDialogs.text(requireContext(), getString(R.string.experiment_recovery_rounds), settings.recoverAfter.toString()) { save(settings.copy(recoverAfter = it.toInt())) }
        }
        page.button(fallback, getString(R.string.experiment_recover)) { feedback(ExperimentRuntime.recover()) }
        statusRow = page.row(fallback, getString(R.string.vpn_probe_result), ExperimentRuntime.status.value.message)
        page.note(getString(R.string.experiment_fallback_note))
        val learned = page.section(getString(R.string.experiment_learned))
        ExperimentRuntime.learned(requireContext()).forEach { domain ->
            page.row(learned, domain, "", R.drawable.ic_route) {
                confirmRemove(requireContext(), R.string.remove_policy_title, domain) {
                    runCatching { ExperimentRuntime.removeLearned(requireContext(), domain); refresh() }.onFailure { feedback(it.message.orEmpty()) }
                }
            }
        }
        if (ExperimentRuntime.learned(requireContext()).isEmpty()) page.row(learned, getString(R.string.experiment_no_learned))
        return page
    }
    private fun feedback(message: String) { view?.let { Snackbar.make(it, message, Snackbar.LENGTH_LONG).show() } }
    override fun onDestroyView() { statusRow = null; super.onDestroyView() }
}
