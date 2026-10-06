package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.appwidget.AppWidgetManager
import android.content.ComponentName
import android.content.Intent
import android.os.Build
import android.provider.Settings
import androidx.activity.result.contract.ActivityResultContracts
import com.google.android.material.snackbar.Snackbar
import com.pidal.sakamoto.R

class SystemSurfacesFragment : androidx.fragment.app.Fragment() {
    private val notificationPermission = registerForActivityResult(ActivityResultContracts.RequestPermission()) { allowed ->
        view?.let { Snackbar.make(it, if (allowed) R.string.notification_enabled else R.string.notification_disabled, Snackbar.LENGTH_LONG).show() }
    }
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext())
        val widget = page.section(getString(R.string.widget_title))
        val variants = listOf(
            Triple(R.string.widget_toggle_label, R.string.widget_toggle_description, com.pidal.sakamoto.widget.VpnToggleWidgetProvider::class.java),
            Triple(R.string.widget_compact_label, R.string.widget_compact_description, com.pidal.sakamoto.widget.VpnCompactWidgetProvider::class.java),
            Triple(R.string.widget_full_label, R.string.widget_description, com.pidal.sakamoto.widget.VpnWidgetProvider::class.java),
        )
        variants.forEach { (title, description, provider) ->
            page.row(widget, getString(title), getString(description), R.drawable.ic_service) {
                val manager = AppWidgetManager.getInstance(requireContext())
                if (Build.VERSION.SDK_INT >= 26 && manager.isRequestPinAppWidgetSupported) manager.requestPinAppWidget(ComponentName(requireContext(), provider), null, null)
                else view?.let { Snackbar.make(it, R.string.widget_manual_add, Snackbar.LENGTH_LONG).show() }
            }
        }
        page.note(getString(R.string.widget_grid_note))
        val notification = page.section(getString(R.string.notification_title))
        page.button(notification, getString(R.string.notification_allow)) {
            if (Build.VERSION.SDK_INT >= 33) notificationPermission.launch(android.Manifest.permission.POST_NOTIFICATIONS)
            else startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, requireContext().packageName))
        }
        page.note(getString(R.string.notification_note))
        return page.root
    }
}
