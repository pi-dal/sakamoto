package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.fragment.app.Fragment
import com.pidal.sakamoto.R
import com.pidal.sakamoto.databinding.FragmentAboutBinding

/**
 * About page — the TUI/iOS About semantics: license, upstream attribution,
 * and the non-affiliation statement. Rendered from build resources; no
 * network, no telemetry.
 */
class AboutFragment : Fragment() {

    private var binding: FragmentAboutBinding? = null

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        val b = FragmentAboutBinding.inflate(inflater, container, false)
        binding = b
        return b.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        val version = try {
            requireContext().packageManager
                .getPackageInfo(requireContext().packageName, 0).versionName
        } catch (e: Exception) {
            "unknown"
        }
        binding?.aboutVersion?.text = getString(R.string.about_version, version ?: "unknown")
    }

    override fun onDestroyView() {
        binding = null
        super.onDestroyView()
    }
}
