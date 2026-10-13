package com.pidal.sakamoto.ui

import android.content.Context
import android.view.View
import android.widget.LinearLayout
import android.widget.TextView
import androidx.core.view.ViewCompat
import com.pidal.sakamoto.R

/** Reopenable setup next to the real controls; status comes from current system checks. */
class LocationSetupGuide(
    private val page: GroupedPage,
    group: LinearLayout,
    openDeviceInfo: () -> Unit,
    openDeveloperOptions: () -> Unit,
    openLocationSettings: () -> Unit,
    allowLocation: () -> Unit,
    openAppSettings: () -> Unit,
    choosePosition: () -> Unit,
    startSimulation: () -> Unit,
    showRoute: () -> Unit,
    checkLocation: () -> Unit
) {
    private val prefs = page.context.getSharedPreferences("virtual-location", Context.MODE_PRIVATE)
    private var expanded = prefs.getBoolean("setupGuideExpanded", true)
    private val summary: TextView
    private val body: LinearLayout
    private val developerStatus: TextView
    private val mockStatus: TextView
    private val servicesStatus: TextView
    private val permissionStatus: TextView
    private val startButton: com.google.android.material.button.MaterialButton
    private val expandButton: com.google.android.material.button.MaterialButton

    init {
        summary = page.paragraph(group, "")
        expandButton = page.button(group, page.context.getString(R.string.location_setup_show)) { setExpanded(!expanded) }
        body = LinearLayout(page.context).apply { orientation = LinearLayout.VERTICAL }
        group.addView(body, LinearLayout.LayoutParams(-1, -2))

        val developer = step(R.string.location_setup_developer_title, R.string.location_setup_developer_body)
        developerStatus = page.paragraph(developer, "")
        page.button(developer, page.context.getString(R.string.location_setup_device_info), action = openDeviceInfo)
        page.button(developer, page.context.getString(R.string.location_developer_options), action = openDeveloperOptions)

        val mock = step(R.string.location_setup_mock_title, R.string.location_setup_mock_body)
        mockStatus = page.paragraph(mock, "")
        page.button(mock, page.context.getString(R.string.location_developer_options), action = openDeveloperOptions)

        val permission = step(R.string.location_setup_permission_title, R.string.location_setup_permission_body)
        servicesStatus = page.paragraph(permission, "")
        page.button(permission, page.context.getString(R.string.location_system_settings), action = openLocationSettings)
        permissionStatus = page.paragraph(permission, "")
        page.button(permission, page.context.getString(R.string.location_setup_allow), action = allowLocation)
        page.button(permission, page.context.getString(R.string.location_setup_app_settings), action = openAppSettings)

        val position = step(R.string.location_setup_position_title, R.string.location_setup_position_body)
        page.button(position, page.context.getString(R.string.location_map_choose), action = choosePosition)

        val start = step(R.string.location_setup_start_title, R.string.location_setup_start_body)
        startButton = page.button(start, page.context.getString(R.string.location_setup_start), primary = true, action = startSimulation)
        page.button(start, page.context.getString(R.string.location_setup_route), action = showRoute)

        val verify = step(R.string.location_setup_verify_title, R.string.location_setup_verify_body)
        page.button(verify, page.context.getString(R.string.location_observe), action = checkLocation)
        page.paragraph(verify, page.context.getString(R.string.location_setup_stop_note))
        setExpanded(expanded)
    }

    private fun step(title: Int, instructions: Int): LinearLayout {
        val group = LinearLayout(page.context).apply { orientation = LinearLayout.VERTICAL }
        body.addView(group, LinearLayout.LayoutParams(-1, -2))
        page.divider(group)
        page.paragraph(group, page.context.getString(title)).apply {
            setTextAppearance(com.google.android.material.R.style.TextAppearance_Material3_TitleMedium)
            ViewCompat.setAccessibilityHeading(this, true)
        }
        page.paragraph(group, page.context.getString(instructions))
        return group
    }

    fun setExpanded(value: Boolean) {
        expanded = value
        body.visibility = if (value) View.VISIBLE else View.GONE
        expandButton.setText(if (value) R.string.location_setup_hide else R.string.location_setup_show)
        prefs.edit().putBoolean("setupGuideExpanded", value).apply()
    }

    fun render(developerEnabled: Boolean, authorized: Boolean, locationEnabled: Boolean, permissionGranted: Boolean, phase: String) {
        developerStatus.setText(if (developerEnabled) R.string.location_setup_developer_ready else R.string.location_setup_developer_needed)
        mockStatus.setText(if (authorized) R.string.location_setup_mock_ready else R.string.location_setup_mock_needed)
        servicesStatus.setText(if (locationEnabled) R.string.location_setup_services_ready else R.string.location_setup_services_needed)
        permissionStatus.setText(if (permissionGranted) R.string.location_setup_permission_ready else R.string.location_setup_permission_needed)
        summary.setText(when {
            !authorized && !developerEnabled -> R.string.location_setup_next_developer
            !authorized -> R.string.location_setup_next_mock
            !locationEnabled || !permissionGranted -> R.string.location_setup_next_permission
            phase == "Active" -> R.string.location_setup_next_verify
            else -> R.string.location_setup_next_start
        })
        startButton.isEnabled = authorized && locationEnabled && permissionGranted && phase !in listOf("Starting", "Stopping")
    }
}
