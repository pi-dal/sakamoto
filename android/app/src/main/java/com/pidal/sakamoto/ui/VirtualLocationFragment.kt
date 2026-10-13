package com.pidal.sakamoto.ui

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.location.Location
import android.location.LocationListener
import android.location.LocationManager
import android.os.Build
import android.os.Bundle
import android.os.CancellationSignal
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.text.InputType
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.TextView
import androidx.activity.OnBackPressedCallback
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat
import androidx.core.location.LocationCompat
import androidx.core.view.doOnNextLayout
import androidx.core.widget.doAfterTextChanged
import androidx.fragment.app.Fragment
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import com.google.android.gms.common.ConnectionResult
import com.google.android.gms.common.GoogleApiAvailability
import com.google.android.gms.location.LocationServices
import com.google.android.gms.location.Priority
import com.google.android.gms.tasks.CancellationTokenSource
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.materialswitch.MaterialSwitch
import com.google.android.material.textfield.TextInputEditText
import com.pidal.sakamoto.R
import com.pidal.sakamoto.bg.MockLocationService
import com.pidal.sakamoto.runtime.MockCoordinates
import com.pidal.sakamoto.runtime.MockLocationRuntime
import com.pidal.sakamoto.runtime.MockLocationStore
import com.pidal.sakamoto.runtime.MockLocationRoute
import com.google.android.material.button.MaterialButton
import kotlinx.coroutines.launch
import java.text.DateFormat
import java.util.Date
import java.util.Locale

class VirtualLocationFragment : Fragment() {
    private data class Controls(val page: GroupedPage, val toggle: MaterialSwitch, val status: TextView, val latitude: TextInputEditText, val longitude: TextInputEditText, val accuracy: TextInputEditText, val message: TextView, val observed: TextView, val routePoints: TextInputEditText, val duration: TextInputEditText, val interval: TextInputEditText, val routeStatus: TextView, val routeStart: MaterialButton, val routePause: MaterialButton, val routeHold: MaterialButton, val selectedPosition: TextView, val routeDraft: TextView)
    private var ui: Controls? = null
    private var setupGuide: LocationSetupGuide? = null
    private var guideVisible = false
    private var guideBack: OnBackPressedCallback? = null
    private var rendering = false
    private var pendingAction = ""
    private val handler = Handler(Looper.getMainLooper())
    private var signal: CancellationSignal? = null
    private var googleToken: CancellationTokenSource? = null
    private var listener: LocationListener? = null
    private var observationActive = false
    private var observationEpoch = 0L
    private val permissions = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
        if (hasLocationPermission()) {
            when (pendingAction) { "observe" -> observe(); "route" -> startRoute(); "start" -> start() }
        } else ui?.message?.text = getString(R.string.location_permission_denied)
        pendingAction = ""
        render()
    }
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext()); val store = MockLocationStore(requireContext()); val saved = store.load()
        val guideContainer = page.section(getString(R.string.location_setup_title))
        page.button(guideContainer, getString(R.string.location_setup_show)) { showGuide(true) }
        val run = page.section(getString(R.string.virtual_location))
        val toggle = page.toggle(run, getString(R.string.location_enable), false)
        val status = page.paragraph(run, "")
        val message = page.note(getString(R.string.location_android_explanation))
        page.button(run, getString(R.string.location_stop)) { runCatching { MockLocationService.stop(requireContext()) }.onFailure { message.text = it.message } }
        val position = page.section(getString(R.string.location_position))
        page.button(position, getString(R.string.location_map_choose)) { openMap(false) }
        val selectedPosition = page.paragraph(position, "")
        val lat = page.field(position, getString(R.string.location_latitude), saved.latitude.toString())
        val lon = page.field(position, getString(R.string.location_longitude), saved.longitude.toString())
        val accuracy = page.field(position, getString(R.string.location_accuracy), saved.accuracy.toString())
        listOf(lat, lon).forEach { it.inputType = InputType.TYPE_CLASS_NUMBER or InputType.TYPE_NUMBER_FLAG_DECIMAL or InputType.TYPE_NUMBER_FLAG_SIGNED }
        accuracy.inputType = InputType.TYPE_CLASS_NUMBER
        lat.parent.let { (it as View).visibility = View.GONE }; lon.parent.let { (it as View).visibility = View.GONE }
        page.button(position, getString(R.string.location_map_advanced)) {
            val show = (lat.parent as View).visibility != View.VISIBLE
            (lat.parent as View).visibility = if (show) View.VISIBLE else View.GONE
            (lon.parent as View).visibility = if (show) View.VISIBLE else View.GONE
        }
        listOf(lat, lon).forEach { field -> field.doAfterTextChanged { updateDraftLabels() } }
        page.button(position, getString(R.string.location_apply), primary = true) {
            try {
                val point = proposed(); store.save(point)
                if (MockLocationRuntime.snapshot.value.phase in listOf("Active", "Starting")) MockLocationService.start(requireContext(), point)
                message.text = getString(R.string.location_coordinates_saved)
            } catch (error: Exception) { message.text = error.message }
        }
        page.button(position, getString(R.string.location_save_favorite)) {
            val field = TextInputEditText(requireContext()).apply { hint = getString(R.string.location_favorite_name); isSingleLine = true }
            MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.location_save_favorite).setView(field)
                .setPositiveButton(R.string.save) { _, _ ->
                    try { store.saveFavorite(field.text.toString(), proposed()); message.text = getString(R.string.location_favorite_saved) }
                    catch (error: Exception) { message.text = error.message }
                }.setNegativeButton(android.R.string.cancel, null).show()
        }
        page.button(position, getString(R.string.location_choose_favorite)) {
            val values = store.favorites()
            if (values.isEmpty()) { message.text = getString(R.string.location_no_favorites); return@button }
            MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.location_choose_favorite).setItems(values.map { it.name }.toTypedArray()) { _, index ->
                val favorite = values[index]; lat.setText(favorite.point.latitude.toString()); lon.setText(favorite.point.longitude.toString()); accuracy.setText(favorite.point.accuracy.toString())
                message.text = getString(R.string.location_favorite_selected)
            }.setNegativeButton(android.R.string.cancel, null).show()
        }
        page.button(position, getString(R.string.location_delete_favorite)) {
            val values = store.favorites(); if (values.isEmpty()) { message.text = getString(R.string.location_no_favorites); return@button }
            MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.location_delete_favorite).setItems(values.map { it.name }.toTypedArray()) { _, index -> store.removeFavorite(values[index].name) }.show()
        }
        val route = page.section(getString(R.string.location_route))
        page.button(route, getString(R.string.location_map_edit_route)) { openMap(true) }
        val routeDraft = page.paragraph(route, "")
        val draft = store.loadRoute()
        val routePoints = page.field(route, getString(R.string.location_route_points), draft?.points?.joinToString("\n") { "${it.latitude}, ${it.longitude}" } ?: "").apply {
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
            isSingleLine = false; minLines = 3; maxLines = 8; gravity = android.view.Gravity.TOP
            filters = arrayOf(android.text.InputFilter.LengthFilter(4096))
        }
        (routePoints.parent as View).visibility = View.GONE
        page.button(route, getString(R.string.location_map_advanced_route)) {
            val view = routePoints.parent as View; view.visibility = if (view.visibility == View.VISIBLE) View.GONE else View.VISIBLE
        }
        routePoints.doAfterTextChanged { updateDraftLabels() }
        val duration = page.field(route, getString(R.string.location_route_duration), (draft?.durationSeconds ?: 300).toString()).apply { inputType = InputType.TYPE_CLASS_NUMBER }
        val interval = page.field(route, getString(R.string.location_route_interval), (draft?.intervalSeconds ?: 1).toString()).apply { inputType = InputType.TYPE_CLASS_NUMBER }
        val routeStatus = page.paragraph(route, getString(R.string.location_route_none))
        val routeStart = page.button(route, getString(R.string.location_route_start), primary = true) {
            if (!hasLocationPermission()) { pendingAction = "route"; requestPermission() } else startRoute()
        }
        val routePause = page.button(route, getString(R.string.location_route_pause)) {
            runCatching { MockLocationService.pauseRoute(requireContext(), MockLocationRuntime.snapshot.value.routeState != "Paused") }.onFailure { message.text = it.message }
        }
        val routeHold = page.button(route, getString(R.string.location_route_hold)) { runCatching { MockLocationService.holdRoute(requireContext()) }.onFailure { message.text = it.message } }
        page.note(getString(R.string.location_route_note))
        val auth = page.section(getString(R.string.location_authorization))
        page.paragraph(auth, getString(R.string.location_developer_instructions))
        page.button(auth, getString(R.string.location_developer_options)) { openSystemSettings(Settings.ACTION_APPLICATION_DEVELOPMENT_SETTINGS) }
        page.button(auth, getString(R.string.location_system_settings)) { openSystemSettings(Settings.ACTION_LOCATION_SOURCE_SETTINGS) }
        val diagnostics = page.section(getString(R.string.location_diagnostics))
        val observed = page.paragraph(diagnostics, getString(R.string.location_not_measured))
        page.button(diagnostics, getString(R.string.location_observe)) {
            if (!hasLocationPermission()) { pendingAction = "observe"; requestPermission() } else observe()
        }
        page.button(diagnostics, getString(R.string.location_setup_app_settings)) { openSystemSettings(Settings.ACTION_APPLICATION_DETAILS_SETTINGS) }
        page.button(diagnostics, getString(R.string.location_share)) { share() }
        page.note(getString(R.string.location_android_diagnostics_note))
        ui = Controls(page, toggle, status, lat, lon, accuracy, message, observed, routePoints, duration, interval, routeStatus, routeStart, routePause, routeHold, selectedPosition, routeDraft)
        setupGuide = LocationSetupGuide(page,
            close = { showGuide(false) },
            openDeviceInfo = { openSystemSettings(Settings.ACTION_DEVICE_INFO_SETTINGS) },
            openDeveloperOptions = { openSystemSettings(Settings.ACTION_APPLICATION_DEVELOPMENT_SETTINGS) },
            openLocationSettings = { openSystemSettings(Settings.ACTION_LOCATION_SOURCE_SETTINGS) },
            allowLocation = { pendingAction = "permission"; requestPermission() },
            openAppSettings = { openSystemSettings(Settings.ACTION_APPLICATION_DETAILS_SETTINGS) },
            choosePosition = { openMap(false) },
            startSimulation = { start(); render() },
            stopSimulation = { runCatching { MockLocationService.stop(requireContext()) }.onFailure { message.text = it.message }; render() },
            showRoute = { jumpTo(route) },
            checkLocation = {
                if (!hasLocationPermission()) { pendingAction = "observe"; requestPermission() } else observe()
            },
            showDiagnostics = { jumpTo(diagnostics) },
            onStepChanged = { cancelObservation() })
        observed.doAfterTextChanged { setupGuide?.setObservation(observed.text.toString()) }
        message.doAfterTextChanged {
            setupGuide?.setFeedback(message.text.toString().takeUnless { it == getString(R.string.location_android_explanation) } ?: "")
        }
        updateDraftLabels()
        toggle.setOnCheckedChangeListener { _, enabled ->
            if (rendering) return@setOnCheckedChangeListener
            if (enabled) {
                if (!hasLocationPermission()) { pendingAction = "start"; requestPermission() } else start()
            } else runCatching { MockLocationService.stop(requireContext()) }.onFailure { message.text = it.message }
            render()
        }
        val root = FrameLayout(requireContext()).apply {
            addView(page.root, FrameLayout.LayoutParams(-1, -1))
            addView(setupGuide!!.root, FrameLayout.LayoutParams(-1, -1))
        }
        val prefs = requireContext().getSharedPreferences("virtual-location", android.content.Context.MODE_PRIVATE)
        showGuide(state?.getBoolean("setupGuideVisible") ?: !prefs.getBoolean("setupWizardSeen", false))
        return root
    }
    override fun onViewCreated(view: View, state: Bundle?) {
        guideBack = object : OnBackPressedCallback(guideVisible) {
            override fun handleOnBackPressed() { setupGuide?.back() }
        }.also { requireActivity().onBackPressedDispatcher.addCallback(viewLifecycleOwner, it) }
        parentFragmentManager.setFragmentResultListener(LocationMapFragment.RESULT, viewLifecycleOwner) { _, result ->
            val controls = ui ?: return@setFragmentResultListener
            val latitudes = result.getDoubleArray("latitudes") ?: return@setFragmentResultListener
            val longitudes = result.getDoubleArray("longitudes") ?: return@setFragmentResultListener
            if (result.getBoolean("route")) {
                controls.routePoints.setText(latitudes.indices.joinToString("\n") { "${latitudes[it]}, ${longitudes[it]}" })
                runCatching { MockLocationStore(requireContext()).saveRoute(MockLocationRoute.parse(controls.routePoints.text.toString(), controls.duration.text.toString(), controls.interval.text.toString(), 25)) }
                controls.message.text = getString(R.string.location_map_route_selected)
            } else if (latitudes.isNotEmpty() && longitudes.isNotEmpty()) {
                controls.latitude.setText(latitudes[0].toString()); controls.longitude.setText(longitudes[0].toString())
                controls.message.text = getString(R.string.location_map_position_selected)
            }
            updateDraftLabels()
        }
        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(Lifecycle.State.STARTED) { MockLocationRuntime.snapshot.collect { render() } }
        }
    }
    override fun onResume() { super.onResume(); guideBack?.isEnabled = guideVisible; render() }
    override fun onPause() { guideBack?.isEnabled = false; super.onPause() }
    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putBoolean("setupGuideVisible", guideVisible)
    }
    private fun showGuide(show: Boolean) {
        guideVisible = show
        setupGuide?.root?.visibility = if (show) View.VISIBLE else View.GONE
        ui?.page?.root?.visibility = if (show) View.GONE else View.VISIBLE
        guideBack?.isEnabled = show && isResumed
        if (show) requireContext().getSharedPreferences("virtual-location", android.content.Context.MODE_PRIVATE).edit().putBoolean("setupWizardSeen", true).apply()
    }
    private fun openSystemSettings(action: String) {
        val intent = Intent(action)
        if (action == Settings.ACTION_APPLICATION_DETAILS_SETTINGS) {
            intent.data = android.net.Uri.fromParts("package", requireContext().packageName, null)
        }
        runCatching { startActivity(intent) }.onFailure {
            ui?.message?.text = getString(R.string.location_settings_unavailable)
            runCatching { startActivity(Intent(Settings.ACTION_SETTINGS)) }
        }
    }
    private fun jumpTo(view: View) {
        val page = ui?.page ?: return
        val content = page.root.getChildAt(0)
        content.doOnNextLayout {
            if (ui?.page !== page) return@doOnNextLayout
            val rect = android.graphics.Rect(0, 0, view.width, view.height)
            page.root.offsetDescendantRectToMyCoords(view, rect)
            page.root.smoothScrollTo(0, rect.top)
        }
        showGuide(false)
        content.requestLayout()
    }
    private fun openMap(route: Boolean) {
        val controls = ui ?: return
        try {
            val center = runCatching { MockCoordinates.parse(controls.latitude.text.toString(), controls.longitude.text.toString(), "25") }.getOrElse { MockLocationStore(requireContext()).load() }
            val points = if (route) {
                if (controls.routePoints.text.toString().isBlank()) emptyList()
                else MockLocationRoute.parse(controls.routePoints.text.toString(), "300", "1", 25).points
            } else listOf(center)
            (requireActivity() as com.pidal.sakamoto.MainActivity).openChild(LocationMapFragment.create(route, points, center), getString(if (route) R.string.location_map_edit_route else R.string.location_map_choose))
        } catch (_: Exception) { controls.message.text = getString(R.string.location_map_invalid_draft) }
    }
    private fun updateDraftLabels() {
        val controls = ui ?: return
        val point = runCatching { MockCoordinates.parse(controls.latitude.text.toString(), controls.longitude.text.toString(), "25") }.getOrNull()
        controls.selectedPosition.text = point?.let { getString(R.string.location_map_selected_coordinates, it.latitude, it.longitude) } ?: getString(R.string.location_map_invalid_coordinates)
        setupGuide?.setPosition(controls.selectedPosition.text.toString())
        val route = runCatching { MockLocationRoute.parse(controls.routePoints.text.toString(), "300", "1", 25) }.getOrNull()
        controls.routeDraft.text = route?.let { getString(R.string.location_map_route_count, it.points.size, it.distance / 1000) } ?: getString(R.string.location_map_route_empty)
    }
    private fun proposed(): MockCoordinates {
        val controls = ui ?: error("Location page unavailable")
        return MockCoordinates.parse(controls.latitude.text.toString(), controls.longitude.text.toString(), controls.accuracy.text.toString())
    }
    private fun start() {
        try {
            check(MockLocationRuntime.authorized(requireContext())) { getString(R.string.location_developer_instructions) }
            MockLocationService.start(requireContext(), proposed())
        } catch (error: Exception) { ui?.message?.text = error.message }
    }
    private fun startRoute() {
        try {
            val controls = ui ?: return
            check(MockLocationRuntime.authorized(requireContext())) { getString(R.string.location_developer_instructions) }
            val meters = controls.accuracy.text.toString().trim().toIntOrNull() ?: error("Enter a whole-number accuracy")
            MockLocationService.startRoute(requireContext(), MockLocationRoute.parse(controls.routePoints.text.toString(), controls.duration.text.toString(), controls.interval.text.toString(), meters))
        } catch (error: Exception) { ui?.message?.text = error.message }
    }
    private fun render() {
        val controls = ui ?: return; val snapshot = MockLocationRuntime.snapshot.value
        val locationEnabled = androidx.core.location.LocationManagerCompat.isLocationEnabled(requireContext().getSystemService(LocationManager::class.java))
        setupGuide?.render(
            Settings.Global.getInt(requireContext().contentResolver, Settings.Global.DEVELOPMENT_SETTINGS_ENABLED, 0) != 0,
            MockLocationRuntime.authorized(requireContext()), locationEnabled, hasLocationPermission(), snapshot.phase)
        rendering = true; controls.toggle.isChecked = snapshot.phase == "Active" || snapshot.phase == "Starting"
        controls.toggle.isEnabled = snapshot.phase != "Starting" && snapshot.phase != "Stopping"; rendering = false
        val time = if (snapshot.lastFix > 0) DateFormat.getDateTimeInstance().format(Date(snapshot.lastFix)) else getString(R.string.location_no_fix)
        controls.status.text = getString(R.string.location_android_status, snapshot.phase, snapshot.fixes, time, if (snapshot.fused) "Google fused + GPS/network" else "GPS/network", if (MockLocationRuntime.authorized(requireContext())) "Granted" else "Choose Sakamoto")
        controls.routeStatus.text = if (snapshot.routeState.isEmpty()) getString(R.string.location_route_none)
            else "${snapshot.routeState} · ${snapshot.routeElapsed.toInt()} / ${snapshot.routeDuration} s\nCurrent WGS84: ${snapshot.point?.latitude}, ${snapshot.point?.longitude}"
        controls.routeStart.isEnabled = snapshot.phase !in listOf("Starting", "Stopping")
        controls.routePause.text = getString(if (snapshot.routeState == "Paused") R.string.location_route_resume else R.string.location_route_pause)
        controls.routePause.isEnabled = snapshot.phase == "Active" && snapshot.routeState in listOf("Moving", "Paused")
        controls.routeHold.isEnabled = snapshot.phase == "Active" && snapshot.routeState.isNotEmpty()
        if (snapshot.error.isNotEmpty()) controls.message.text = snapshot.error
        else if (snapshot.phase != "Active" && snapshot.phase != "Starting" && MockLocationStore(requireContext()).needsRecovery()) controls.message.text = "A previous simulation needs cleanup. Tap Stop simulation before starting again."
    }
    private fun hasLocationPermission() = ContextCompat.checkSelfPermission(requireContext(), Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED || ContextCompat.checkSelfPermission(requireContext(), Manifest.permission.ACCESS_COARSE_LOCATION) == PackageManager.PERMISSION_GRANTED
    private fun requestPermission() = permissions.launch(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION))
    @Suppress("MissingPermission", "DEPRECATION")
    private fun observe() {
        cancelObservation(); observationActive = true
        val epoch = observationEpoch
        ui?.observed?.text = getString(R.string.location_observing)
        handler.postDelayed({ if (observationActive && observationEpoch == epoch) { cancelObservation(); ui?.observed?.text = getString(R.string.location_observation_timeout) } }, 10000)
        try {
            if (GoogleApiAvailability.getInstance().isGooglePlayServicesAvailable(requireContext()) == ConnectionResult.SUCCESS) {
                googleToken = CancellationTokenSource()
                LocationServices.getFusedLocationProviderClient(requireContext()).getCurrentLocation(Priority.PRIORITY_HIGH_ACCURACY, googleToken!!.token)
                    .addOnSuccessListener { fix -> if (observationActive && observationEpoch == epoch) { if (fix != null) observedFix(fix) else { cancelObservation(); ui?.observed?.text = getString(R.string.location_observation_timeout) } } }
                    .addOnFailureListener { if (observationActive && observationEpoch == epoch) { cancelObservation(); ui?.observed?.text = getString(R.string.location_observation_timeout) } }
            } else {
                val manager = requireContext().getSystemService(LocationManager::class.java)
                if (Build.VERSION.SDK_INT >= 30) {
                    signal = CancellationSignal(); manager.getCurrentLocation(LocationManager.GPS_PROVIDER, signal, requireContext().mainExecutor) { fix -> if (observationActive && observationEpoch == epoch && fix != null) observedFix(fix) }
                } else {
                    listener = object : LocationListener {
                        override fun onLocationChanged(location: Location) { if (observationActive && observationEpoch == epoch) observedFix(location) }
                        override fun onProviderEnabled(provider: String) {}
                        override fun onProviderDisabled(provider: String) {}
                        override fun onStatusChanged(provider: String?, status: Int, extras: Bundle?) {}
                    }
                    manager.requestSingleUpdate(LocationManager.GPS_PROVIDER, listener!!, Looper.getMainLooper())
                }
            }
        } catch (_: Exception) { cancelObservation(); ui?.observed?.text = getString(R.string.location_observation_timeout) }
    }
    private fun observedFix(fix: Location) {
        cancelObservation()
        val point = MockLocationRuntime.snapshot.value.point ?: MockLocationStore(requireContext()).load()
        val target = Location("selected").apply { latitude = point.latitude; longitude = point.longitude }
        ui?.observed?.text = String.format(Locale.US, "%.6f, %.6f · ±%.0f m\nMock flag: %s · distance from selected: %.0f m\nFix time: %s", fix.latitude, fix.longitude, fix.accuracy, LocationCompat.isMock(fix), fix.distanceTo(target), DateFormat.getDateTimeInstance().format(Date(fix.time)))
    }
    private fun cancelObservation() {
        observationActive = false; observationEpoch++; handler.removeCallbacksAndMessages(null); signal?.cancel(); signal = null; googleToken?.cancel(); googleToken = null
        listener?.let { runCatching { context?.getSystemService(LocationManager::class.java)?.removeUpdates(it) } }; listener = null
    }
    private fun share() {
        val snapshot = MockLocationRuntime.snapshot.value; val point = snapshot.point ?: MockLocationStore(requireContext()).load()
        val version = runCatching { requireContext().packageManager.getPackageInfo(requireContext().packageName, 0).versionName }.getOrDefault("unknown")
        val report = "Sakamoto $version · Android ${Build.VERSION.RELEASE}\nVirtual location · ${Date()}\nWGS84: ${point.latitude}, ${point.longitude}; accuracy: ${point.accuracy} m\nStatus: ${snapshot.phase}; injected fixes: ${snapshot.fixes}; fused mock: ${snapshot.fused}; last fix: ${snapshot.lastFix}\nRoute: ${snapshot.routeState}; elapsed: ${snapshot.routeElapsed} / ${snapshot.routeDuration} s\nDetail: ${snapshot.error}\nObserved: ${ui?.observed?.text}"
        startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, report), getString(R.string.location_share)))
    }
    override fun onDestroyView() { cancelObservation(); guideBack = null; setupGuide = null; ui = null; super.onDestroyView() }
}
