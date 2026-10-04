package com.pidal.sakamoto.ui

import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.button.MaterialButton
import com.pidal.sakamoto.R
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.databinding.FragmentTailscaleBinding
import com.pidal.sakamoto.runtime.TailscaleCapabilities
import com.pidal.sakamoto.runtime.TailscaleRuntime
import com.pidal.sakamoto.runtime.TailscaleUiState
import com.pidal.sakamoto.security.TailscaleAuthKeyStore
import kotlinx.coroutines.launch

/**
 * Built-in Tailscale page — the Android counterpart of the iOS TailscaleView.
 *
 * What is REAL here (each maps to a libbox v1.14.2 command RPC, issued by
 * CommandClientRuntime): live tailnet status via SubscribeTailscaleStatus,
 * exit-node pick/clear via SetTailscaleExitNode, logout via TailscaleLogout,
 * per-peer latency via StartTailscalePing. The auth URL opens the system
 * browser for the login flow; the optional auth key is stored
 * Keystore-encrypted in app-private storage (TailscaleAuthKeyStore) and is
 * injected into the endpoint config only at start/reload time
 * (TailscaleConfigInjection) — it never renders in full and never reaches a
 * log line.
 *
 * What is explicitly unsupported is rendered verbatim from
 * TailscaleCapabilities.unsupported (Taildrop, Tailscale SSH, serve, auth-key
 * login UI, external CLI probe) — build facts and API facts, not omissions.
 *
 * STATUS PENDING SDK BUILD VERIFICATION — android/README.md.
 */
class TailscaleFragment : Fragment() {

    private var binding: FragmentTailscaleBinding? = null

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        val b = FragmentTailscaleBinding.inflate(inflater, container, false)
        binding = b
        b.openAuthUrl.setOnClickListener { openAuthURL() }
        b.authkeyStore.setOnClickListener { promptStoreKey() }
        b.authkeyDelete.setOnClickListener { deleteKey() }
        b.clearExitNode.setOnClickListener { clearExitNode() }
        b.pingExit.setOnClickListener { pingExitNode() }
        b.logout.setOnClickListener { confirmLogout() }
        return b.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        refreshAuthKeyState()
        // The status stream rides the command channel; open it when the page
        // shows (the channel itself follows the tunnel lifecycle).
        CommandClientRuntime.start()
        CommandClientRuntime.subscribeTailscaleStatus()
        viewLifecycleOwner.lifecycleScope.launch {
            TailscaleRuntime.state.collect { state -> render(state) }
        }
    }

    override fun onDestroyView() {
        binding = null
        super.onDestroyView()
    }

    private fun refreshAuthKeyState() {
        val store = TailscaleAuthKeyStore(requireContext())
        val key = store.readAuthKey()
        TailscaleRuntime.setAuthKeyState(
            hasKey = key != null,
            masked = key?.let { com.pidal.sakamoto.runtime.TailscaleAuthKeyMasking.mask(it) } ?: "",
        )
    }

    private fun render(state: TailscaleUiState) {
        val b = binding ?: return
        val primary = state.primary
        b.backendState.text = primary?.backendState?.wireString ?: getString(R.string.ts_not_subscribed)
        b.tailnetSummary.text = primary?.let {
            getString(R.string.ts_tailnet_line, it.networkName.ifEmpty { "—" }, it.magicDNSSuffix.ifEmpty { "—" })
        } ?: ""
        b.exitNodeSummary.text = primary?.exitNodePeer?.let {
            getString(R.string.ts_exit_line, it.displayName)
        } ?: getString(R.string.ts_exit_none)

        val showLogin = primary != null && primary.backendState.needsLoginFlow && state.authURL.isNotEmpty()
        b.openAuthUrl.visibility = if (showLogin) View.VISIBLE else View.GONE

        b.authkeyState.text = if (state.hasAuthKey) {
            getString(R.string.ts_authkey_stored, state.authKeyMasked)
        } else {
            getString(R.string.ts_authkey_absent)
        }

        renderCandidates(primary)

        b.peersSummary.text = primary?.peers?.joinToString("\n") { peer ->
            val flags = buildList {
                if (peer.online) add("online")
                if (peer.active) add("active")
                if (peer.expired) add("expired")
                if (peer.exitNodeOption) add("exit-candidate")
            }
            "• ${peer.displayName} (${peer.os})" + if (flags.isEmpty()) "" else "  [${flags.joinToString(", ")}]"
        } ?: ""

        b.pingResults.text = state.pingLines.joinToString("\n")
        b.unsupportedList.text = TailscaleCapabilities.unsupported.joinToString("\n") {
            "• ${it.capability}: ${it.reason}"
        }
        b.tsNotice.text = state.notice ?: ""
    }

    private fun renderCandidates(primary: com.pidal.sakamoto.runtime.TailscaleEndpointSummary?) {
        val b = binding ?: return
        b.exitCandidatesContainer.removeAllViews()
        val candidates = primary?.exitNodeCandidates.orEmpty()
        if (candidates.isEmpty()) {
            val empty = TextView(requireContext())
            empty.text = getString(R.string.ts_no_candidates)
            b.exitCandidatesContainer.addView(empty)
            return
        }
        val endpointTag = primary.endpointTag
        for (candidate in candidates) {
            val button = MaterialButton(requireContext())
            button.text = getString(R.string.ts_candidate_button, candidate.displayName)
            button.setOnClickListener {
                CommandClientRuntime.setTailscaleExitNode(endpointTag, candidate.stableID)
            }
            b.exitCandidatesContainer.addView(button)
        }
    }

    private fun openAuthURL() {
        val url = TailscaleRuntime.state.value.authURL
        if (url.isEmpty()) return
        try {
            startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)))
        } catch (e: ActivityNotFoundException) {
            TailscaleRuntime.setNotice(getString(R.string.ts_no_browser))
        }
    }

    private fun promptStoreKey() {
        val context = requireContext()
        val input = EditText(context)
        input.hint = getString(R.string.ts_authkey_hint)
        input.inputType = android.text.InputType.TYPE_CLASS_TEXT or
            android.text.InputType.TYPE_TEXT_VARIATION_PASSWORD
        val container = LinearLayout(context)
        container.setPadding(48, 24, 48, 0)
        container.addView(input)
        MaterialAlertDialogBuilder(context)
            .setTitle(R.string.ts_authkey_store)
            .setView(container)
            .setPositiveButton(R.string.ts_authkey_store) { _, _ ->
                val key = input.text?.toString()?.trim().orEmpty()
                if (key.isNotEmpty()) {
                    try {
                        TailscaleAuthKeyStore(context).storeAuthKey(key)
                    } catch (e: Exception) {
                        TailscaleRuntime.setNotice("store failed: ${e.message}")
                    }
                }
                input.setText("") // never keep the secret in the widget
                refreshAuthKeyState()
            }
            .setNegativeButton(android.R.string.cancel) { dialog, _ -> input.setText(""); dialog.dismiss() }
            .show()
    }

    private fun deleteKey() {
        TailscaleAuthKeyStore(requireContext()).deleteAuthKey()
        refreshAuthKeyState()
    }

    private fun clearExitNode() {
        val primary = TailscaleRuntime.state.value.primary ?: return
        CommandClientRuntime.setTailscaleExitNode(primary.endpointTag, "")
    }

    private fun pingExitNode() {
        val primary = TailscaleRuntime.state.value.primary ?: return
        val target = primary.exitNodePingTarget
        if (target == null) {
            TailscaleRuntime.setNotice(getString(R.string.ts_no_ping_target))
            return
        }
        CommandClientRuntime.startTailscalePing(primary.endpointTag, target)
    }

    private fun confirmLogout() {
        val primary = TailscaleRuntime.state.value.primary ?: return
        MaterialAlertDialogBuilder(requireContext())
            .setTitle(R.string.ts_logout)
            .setMessage(R.string.ts_logout_confirm)
            .setPositiveButton(R.string.ts_logout) { _, _ ->
                CommandClientRuntime.tailscaleLogout(primary.endpointTag)
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }
}
