package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import com.pidal.sakamoto.R
import com.pidal.sakamoto.databinding.FragmentSettingsBinding
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import kotlinx.coroutines.launch

/**
 * Settings page skeleton. The TUI/iOS split applies unchanged:
 *   - editable on device: generated-config knobs (log level, block QUIC/STUN)
 *     — applied through the ConfigStore/reload path;
 *   - host-owned: chain exits, fallbacks, subscriptions (sakamoto.yaml lives
 *     on the sakamoto host and is never edited or synced from the device).
 *
 * The editable knobs reuse the ConfigStore path (config JSON merges) and are
 * a follow-up form here; the page renders live state and the boundary.
 *
 * STATUS PENDING SDK BUILD VERIFICATION — android/README.md.
 */
class SettingsFragment : Fragment() {

    private var binding: FragmentSettingsBinding? = null

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        val b = FragmentSettingsBinding.inflate(inflater, container, false)
        binding = b
        b.tailscaleEntry.setOnClickListener {
            parentFragmentManager.beginTransaction()
                .replace(R.id.fragment_container, TailscaleFragment())
                .addToBackStack(null)
                .commit()
        }
        return b.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        viewLifecycleOwner.lifecycleScope.launch {
            MobilecoreRuntime.state.collect { state ->
                binding?.runtimeState?.text = getString(
                    R.string.settings_runtime,
                    state.phase,
                    state.routingMode.ifEmpty { "—" },
                    state.configState,
                )
            }
        }
    }

    override fun onDestroyView() {
        binding = null
        super.onDestroyView()
    }
}
