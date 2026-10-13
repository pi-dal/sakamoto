package com.pidal.sakamoto.ui

import android.content.Context
import android.view.Gravity
import android.view.View
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView
import androidx.appcompat.widget.PopupMenu
import androidx.core.view.ViewCompat
import androidx.core.widget.NestedScrollView
import com.google.android.material.button.MaterialButton
import com.google.android.material.progressindicator.LinearProgressIndicator
import com.pidal.sakamoto.R

/** A separate paged surface; its callbacks operate on the location page's draft. */
class LocationSetupGuide(
    private val page: GroupedPage,
    private val close: () -> Unit,
    private val openDeviceInfo: () -> Unit,
    private val openDeveloperOptions: () -> Unit,
    private val openLocationSettings: () -> Unit,
    private val allowLocation: () -> Unit,
    private val openAppSettings: () -> Unit,
    private val choosePosition: () -> Unit,
    private val startSimulation: () -> Unit,
    private val stopSimulation: () -> Unit,
    private val showRoute: () -> Unit,
    private val checkLocation: () -> Unit,
    private val showDiagnostics: () -> Unit,
    private val onStepChanged: () -> Unit
) {
    private val context = page.context
    private val prefs = context.getSharedPreferences("virtual-location", Context.MODE_PRIVATE)
    private val titles = intArrayOf(R.string.location_setup_developer_title, R.string.location_setup_mock_title,
        R.string.location_setup_permission_title, R.string.location_setup_position_title,
        R.string.location_setup_start_title, R.string.location_setup_verify_title)
    private val instructions = intArrayOf(R.string.location_setup_developer_body, R.string.location_setup_mock_body,
        R.string.location_setup_permission_body, R.string.location_setup_position_body,
        R.string.location_setup_start_body, R.string.location_setup_verify_body)
    private var step = prefs.getInt("setupWizardStep", 0).coerceIn(titles.indices)
    private var developerEnabled = false
    private var authorized = false
    private var locationEnabled = false
    private var permissionGranted = false
    private var phase = "Off"
    private var observation: CharSequence = context.getString(R.string.location_not_measured)
    private var position: CharSequence = ""
    private var feedback: CharSequence = ""
    private var statusView: TextView? = null
    private var permissionView: TextView? = null
    private var servicesView: TextView? = null
    private var prerequisiteButton: MaterialButton? = null
    private var developerButton: MaterialButton? = null
    private var developerReviewButton: MaterialButton? = null
    private var observationView: TextView? = null
    private var positionView: TextView? = null
    private var feedbackView: TextView? = null
    private var startButton: MaterialButton? = null
    private var checkButton: MaterialButton? = null
    val root = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        setBackgroundColor(context.getColor(R.color.surface))
    }
    private val progress = LinearProgressIndicator(context).apply { max = titles.size; isIndeterminate = false }
    private val counter = button("", primary = false) { jumpMenu() }
    private val scroll = NestedScrollView(context).apply { isFillViewport = true }
    private val body = LinearLayout(context).apply { orientation = LinearLayout.VERTICAL; setPadding(0, 0, 0, page.dp(20)) }
    private val previous = button(context.getString(R.string.location_setup_previous), primary = false) { select(step - 1) }
    private val next = button("", primary = true) { if (step == titles.lastIndex) close() else select(step + 1) }

    init {
        val header = LinearLayout(context).apply {
            gravity = Gravity.CENTER_VERTICAL
            setPadding(page.dp(12), page.dp(8), page.dp(12), page.dp(8))
        }
        header.addView(counter, LinearLayout.LayoutParams(0, -2, 1f))
        header.addView(button(context.getString(R.string.location_setup_close), primary = false, action = close), LinearLayout.LayoutParams(-2, -2))
        root.addView(header, LinearLayout.LayoutParams(-1, -2))
        root.addView(progress, LinearLayout.LayoutParams(-1, -2).apply { marginStart = page.dp(20); marginEnd = page.dp(20) })
        val bodyContainer = object : FrameLayout(context) {
            override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
                body.layoutParams.width = minOf(View.MeasureSpec.getSize(widthMeasureSpec), page.dp(600))
                super.onMeasure(widthMeasureSpec, heightMeasureSpec)
            }
        }.apply { addView(body, FrameLayout.LayoutParams(-1, -2, Gravity.TOP or Gravity.CENTER_HORIZONTAL)) }
        scroll.addView(bodyContainer, android.view.ViewGroup.LayoutParams(-1, -2))
        root.addView(scroll, LinearLayout.LayoutParams(-1, 0, 1f))
        page.divider(root)
        val footer = LinearLayout(context).apply { setPadding(page.dp(20), page.dp(8), page.dp(20), page.dp(8)) }
        footer.addView(previous, LinearLayout.LayoutParams(0, -2, 1f).apply { marginEnd = page.dp(8) })
        footer.addView(next, LinearLayout.LayoutParams(0, -2, 1f).apply { marginStart = page.dp(8) })
        root.addView(footer, LinearLayout.LayoutParams(-1, -2))
        buildPage()
    }
    private fun button(title: String, primary: Boolean, action: () -> Unit) = MaterialButton(context, null,
        if (primary) com.google.android.material.R.attr.materialButtonStyle else com.google.android.material.R.attr.borderlessButtonStyle
    ).apply {
        text = title; isAllCaps = false; minimumHeight = page.dp(48)
        setOnClickListener { action() }
    }
    private fun select(value: Int) {
        if (value !in titles.indices) return
        step = value
        prefs.edit().putInt("setupWizardStep", step).apply()
        onStepChanged()
        feedback = ""
        buildPage()
        scroll.scrollTo(0, 0)
        scroll.post { if (root.isAttachedToWindow) scroll.scrollTo(0, 0) }
    }
    fun back() { if (step > 0) select(step - 1) else close() }
    private fun jumpMenu() {
        PopupMenu(context, counter).apply {
            titles.forEachIndexed { index, title ->
                menu.add(0, index, index, "${index + 1}. ${context.getString(title)}").apply { isCheckable = true; isChecked = index == step }
            }
            setOnMenuItemClickListener { select(it.itemId); true }
            show()
        }
    }
    private fun buildPage() {
        body.removeAllViews()
        statusView = null; permissionView = null; servicesView = null; prerequisiteButton = null; developerButton = null; developerReviewButton = null; observationView = null; positionView = null; startButton = null; checkButton = null
        counter.text = context.getString(R.string.location_setup_progress, step + 1, titles.size) + " ▾"
        counter.contentDescription = context.getString(R.string.location_setup_jump) + ", " + context.getString(R.string.location_setup_progress, step + 1, titles.size)
        progress.progress = step + 1
        progress.contentDescription = context.getString(R.string.location_setup_progress, step + 1, titles.size)
        previous.isEnabled = step > 0
        next.setText(if (step == titles.lastIndex) R.string.location_setup_done else R.string.location_setup_next)
        page.paragraph(body, context.getString(titles[step])).apply {
            setTextAppearance(com.google.android.material.R.style.TextAppearance_Material3_HeadlineSmall)
            ViewCompat.setAccessibilityHeading(this, true)
        }
        page.paragraph(body, context.getString(instructions[step]))
        when (step) {
            0 -> {
                statusView = page.paragraph(body, "")
                page.button(body, context.getString(R.string.location_setup_device_info), action = openDeviceInfo)
                developerButton = page.button(body, context.getString(R.string.location_developer_options), action = openDeveloperOptions)
            }
            1 -> {
                statusView = page.paragraph(body, "")
                developerButton = page.button(body, context.getString(R.string.location_developer_options), action = openDeveloperOptions)
                developerReviewButton = page.button(body, context.getString(R.string.location_setup_enable_developer_first)) { select(0) }
            }
            2 -> {
                page.paragraph(body, context.getString(R.string.location_setup_keep_services_on))
                statusView = page.paragraph(body, "")
                page.button(body, context.getString(R.string.location_system_settings), action = openLocationSettings)
                permissionView = page.paragraph(body, "")
                page.button(body, context.getString(R.string.location_setup_allow), action = allowLocation)
                page.button(body, context.getString(R.string.location_setup_app_settings), action = openAppSettings)
            }
            3 -> {
                page.button(body, context.getString(R.string.location_map_choose), action = choosePosition)
                positionView = page.paragraph(body, position)
            }
            4 -> {
                statusView = page.paragraph(body, "")
                startButton = page.button(body, context.getString(R.string.location_setup_start), primary = true, action = startSimulation)
                prerequisiteButton = page.button(body, context.getString(R.string.location_setup_review_required)) {
                    select(if (!authorized) 1 else 2)
                }
                page.paragraph(body, context.getString(R.string.location_setup_next_does_not_start))
                page.button(body, context.getString(R.string.location_setup_route), action = showRoute)
            }
            5 -> {
                statusView = page.paragraph(body, "")
                servicesView = page.paragraph(body, "")
                page.button(body, context.getString(R.string.location_system_settings), action = openLocationSettings)
                checkButton = page.button(body, context.getString(R.string.location_observe), action = checkLocation)
                observationView = page.paragraph(body, observation)
                page.button(body, context.getString(R.string.location_setup_diagnostics), action = showDiagnostics)
                page.button(body, context.getString(R.string.location_stop), action = stopSimulation)
                page.paragraph(body, context.getString(R.string.location_setup_stop_note))
            }
        }
        feedbackView = page.paragraph(body, feedback)
        feedbackView?.visibility = if (feedback.isEmpty()) View.GONE else View.VISIBLE
        updateStatus()
    }
    private fun updateStatus() {
        when (step) {
            0 -> statusView?.setText(if (developerEnabled) R.string.location_setup_developer_ready else R.string.location_setup_developer_needed)
            1 -> statusView?.setText(if (authorized) R.string.location_setup_mock_ready else R.string.location_setup_mock_needed)
            2 -> {
                statusView?.setText(if (locationEnabled) R.string.location_setup_services_ready else R.string.location_setup_services_needed)
                permissionView?.setText(if (permissionGranted) R.string.location_setup_permission_ready else R.string.location_setup_permission_needed)
            }
            4 -> {
                val needed = when { !authorized -> R.string.location_setup_mock_needed; !locationEnabled -> R.string.location_setup_services_needed; !permissionGranted -> R.string.location_setup_permission_needed; else -> null }
                statusView?.text = context.getString(R.string.location_setup_simulation_status, phase) + (needed?.let { "\n" + context.getString(it) } ?: "")
                prerequisiteButton?.visibility = if (needed == null) View.GONE else View.VISIBLE
            }
            5 -> {
                statusView?.text = context.getString(R.string.location_setup_simulation_status, phase)
                servicesView?.setText(if (locationEnabled) R.string.location_setup_services_ready else R.string.location_setup_services_needed)
            }
        }
        developerButton?.visibility = if (developerEnabled) View.VISIBLE else View.GONE
        developerReviewButton?.visibility = if (!developerEnabled && !authorized) View.VISIBLE else View.GONE
        startButton?.isEnabled = authorized && locationEnabled && permissionGranted && phase !in listOf("Starting", "Stopping")
        checkButton?.isEnabled = locationEnabled
    }
    fun render(developerEnabled: Boolean, authorized: Boolean, locationEnabled: Boolean, permissionGranted: Boolean, phase: String) {
        this.developerEnabled = developerEnabled; this.authorized = authorized; this.locationEnabled = locationEnabled
        this.permissionGranted = permissionGranted; this.phase = phase
        updateStatus()
    }
    fun setObservation(value: CharSequence) { observation = value; observationView?.text = value }
    fun setPosition(value: CharSequence) { position = value; positionView?.text = value }
    fun setFeedback(value: CharSequence) {
        feedback = value
        feedbackView?.text = value
        feedbackView?.visibility = if (value.isEmpty()) View.GONE else View.VISIBLE
    }
}
