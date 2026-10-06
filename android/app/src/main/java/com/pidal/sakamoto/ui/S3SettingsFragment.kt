package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.lifecycle.lifecycleScope
import com.google.android.material.bottomsheet.BottomSheetDialog
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.S3SyncRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject

/** S3 endpoint form is a child page; secret editing is an explicit modal task. */
class S3SettingsFragment : androidx.fragment.app.Fragment() {
    private var busy = false

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext())
        val repo = S3SyncRepository(requireContext())
        val settings = repo.settings()
        val group = page.section(getString(R.string.s3_section_sync))
        val enabled = page.toggle(group, getString(R.string.s3_enabled), settings.optBoolean("enabled"))
        var confirmingEnable = false
        enabled.setOnCheckedChangeListener { _, checked ->
            if (checked && !confirmingEnable) {
                confirmingEnable = true
                enabled.isChecked = false
                MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.s3_enable_confirm_title)
                    .setMessage(R.string.s3_enable_confirm_message).setNegativeButton(android.R.string.cancel, null)
                    .setPositiveButton(R.string.s3_enable_action) { _, _ -> enabled.isChecked = true }
                    .setOnDismissListener { confirmingEnable = false }.show()
            }
        }
        val status = page.row(group, getString(R.string.s3_status_title), getString(R.string.s3_status_ready), R.drawable.ic_sync)
        val endpoint = page.field(group, getString(R.string.s3_endpoint), settings.optString("endpoint"), uri = true)
        val region = page.field(group, getString(R.string.s3_region), settings.optString("region", "us-east-1"))
        val bucket = page.field(group, getString(R.string.s3_bucket), settings.optString("bucket"))
        val prefix = page.field(group, getString(R.string.s3_prefix), settings.optString("prefix", "sakamoto"))
        val save = page.button(group, getString(R.string.s3_save), primary = true) {
            try {
                repo.save(JSONObject().put("enabled", enabled.isChecked).put("endpoint", endpoint.text.toString().trim()).put("region", region.text.toString().trim()).put("bucket", bucket.text.toString().trim()).put("prefix", prefix.text.toString().trim()), repo.credentials())
                status.detail(getString(R.string.s3_status_saved))
            } catch (e: Exception) { status.detail(getString(R.string.s3_status_error, e.message)) }
        }
        page.note(getString(R.string.s3_sources_note))
        val auth = page.section(getString(R.string.s3_section_credentials))
        val authRow = page.row(auth, getString(R.string.s3_edit_credentials), getString(R.string.s3_keystore_note), R.drawable.ic_info) {
            if (busy) return@row
            val modal = BottomSheetDialog(requireContext())
            val form = GroupedPage(requireContext())
            val fields = form.section(getString(R.string.s3_section_credentials))
            val credentials = repo.credentials()
            val access = form.field(fields, getString(R.string.s3_access_key), credentials.optString("access_key"), secret = true)
            val secret = form.field(fields, getString(R.string.s3_secret_key), credentials.optString("secret_key"), secret = true)
            val token = form.field(fields, getString(R.string.s3_session_token), credentials.optString("session_token"), secret = true)
            form.button(fields, getString(R.string.s3_save_credentials), primary = true) {
                try {
                    repo.save(repo.settings(), JSONObject().put("access_key", access.text.toString()).put("secret_key", secret.text.toString()).put("session_token", token.text.toString()))
                    status.detail(getString(R.string.s3_status_saved)); modal.dismiss()
                } catch (_: Exception) { secret.error = getString(R.string.invalid_value) }
            }
            form.button(fields, getString(android.R.string.cancel)) { modal.dismiss() }
            modal.setOnDismissListener { access.setText(""); secret.setText(""); token.setText("") }
            modal.setContentView(form.root); modal.show()
        }
        val actions = page.section(getString(R.string.s3_section_actions))
        val sync = page.button(actions, getString(R.string.s3_sync_now)) {
            if (!busy) {
                busy = true
                status.detail(getString(R.string.s3_status_syncing))
                save.isEnabled = false; authRow.isEnabled = false
                viewLifecycleOwner.lifecycleScope.launch {
                    try { status.detail(withContext(Dispatchers.IO) { repo.syncNow() }) }
                    catch (e: Exception) { status.detail(getString(R.string.s3_status_error, e.message)) }
                    finally { busy = false; save.isEnabled = true; authRow.isEnabled = true }
                }
            }
        }
        return page.root
    }
}
