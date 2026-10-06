package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import com.pidal.sakamoto.R

/** Credits and legal information as compact read-only grouped rows. */
class AboutFragment : androidx.fragment.app.Fragment() {
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext())
        val app = page.section(getString(R.string.about_section_app))
        val version = try { requireContext().packageManager.getPackageInfo(requireContext().packageName, 0).versionName ?: "unknown" } catch (_: Exception) { "unknown" }
        page.row(app, getString(R.string.about_version_label), getString(R.string.about_version, version), R.drawable.ic_info)
        page.note(getString(R.string.about_unofficial))
        val legal = page.section(getString(R.string.about_section_legal))
        page.paragraph(legal, getString(R.string.about_license))
        page.paragraph(legal, getString(R.string.about_singbox))
        page.paragraph(legal, getString(R.string.about_tailscale))
        val credits = page.section(getString(R.string.about_section_credits))
        page.paragraph(credits, getString(R.string.about_portrait))
        page.paragraph(credits, getString(R.string.about_state_semantics))
        return page.root
    }
}
