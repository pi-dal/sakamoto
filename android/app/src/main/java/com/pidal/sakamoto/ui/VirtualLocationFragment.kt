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
import android.widget.TextView
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat
import androidx.core.location.LocationCompat
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
import kotlinx.coroutines.launch
import java.text.DateFormat
import java.util.Date
import java.util.Locale

class VirtualLocationFragment : Fragment() {
    private data class Controls(val page: GroupedPage, val toggle: MaterialSwitch, val status: TextView, val latitude: TextInputEditText, val longitude: TextInputEditText, val accuracy: TextInputEditText, val message: TextView, val observed: TextView)
    private var ui: Controls? = null
    private var rendering = false
    private var pendingAction = ""
    private val handler = Handler(Looper.getMainLooper())
    private var signal: CancellationSignal? = null
    private var googleToken: CancellationTokenSource? = null
    private var listener: LocationListener? = null
    private var observationActive = false
    private var observationEpoch = 0L
    private val permissions = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
        if (hasLocationPermission()) { if (pendingAction == "observe") observe() else start() }
        else ui?.message?.text = getString(R.string.location_permission_denied)
        pendingAction = ""
    }
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext()); val store = MockLocationStore(requireContext()); val saved = store.load()
        val run = page.section(getString(R.string.virtual_location))
        val toggle = page.toggle(run, getString(R.string.location_enable), false)
        val status = page.paragraph(run, "")
        val message = page.note(getString(R.string.location_android_explanation))
        page.button(run, getString(R.string.location_stop)) { runCatching { MockLocationService.stop(requireContext()) }.onFailure { message.text = it.message } }
        val position = page.section(getString(R.string.location_position))
        val lat = page.field(position, getString(R.string.location_latitude), saved.latitude.toString())
        val lon = page.field(position, getString(R.string.location_longitude), saved.longitude.toString())
        val accuracy = page.field(position, getString(R.string.location_accuracy), saved.accuracy.toString())
        listOf(lat, lon).forEach { it.inputType = InputType.TYPE_CLASS_NUMBER or InputType.TYPE_NUMBER_FLAG_DECIMAL or InputType.TYPE_NUMBER_FLAG_SIGNED }
        accuracy.inputType = InputType.TYPE_CLASS_NUMBER
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
        val auth = page.section(getString(R.string.location_authorization))
        page.paragraph(auth, getString(R.string.location_developer_instructions))
        page.button(auth, getString(R.string.location_developer_options)) { runCatching { startActivity(Intent(Settings.ACTION_APPLICATION_DEVELOPMENT_SETTINGS)) }.onFailure { startActivity(Intent(Settings.ACTION_SETTINGS)) } }
        page.button(auth, getString(R.string.location_system_settings)) { startActivity(Intent(Settings.ACTION_LOCATION_SOURCE_SETTINGS)) }
        val diagnostics = page.section(getString(R.string.location_diagnostics))
        val observed = page.paragraph(diagnostics, getString(R.string.location_not_measured))
        page.button(diagnostics, getString(R.string.location_observe)) {
            if (!hasLocationPermission()) { pendingAction = "observe"; requestPermission() } else observe()
        }
        page.button(diagnostics, getString(R.string.location_share)) { share() }
        page.note(getString(R.string.location_android_diagnostics_note))
        ui = Controls(page, toggle, status, lat, lon, accuracy, message, observed)
        toggle.setOnCheckedChangeListener { _, enabled ->
            if (rendering) return@setOnCheckedChangeListener
            if (enabled) {
                if (!hasLocationPermission()) { pendingAction = "start"; requestPermission() } else start()
            } else runCatching { MockLocationService.stop(requireContext()) }.onFailure { message.text = it.message }
            render()
        }
        return page.root
    }
    override fun onViewCreated(view: View, state: Bundle?) {
        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(Lifecycle.State.STARTED) { MockLocationRuntime.snapshot.collect { render() } }
        }
    }
    override fun onResume() { super.onResume(); render() }
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
    private fun render() {
        val controls = ui ?: return; val snapshot = MockLocationRuntime.snapshot.value
        rendering = true; controls.toggle.isChecked = snapshot.phase == "Active" || snapshot.phase == "Starting"
        controls.toggle.isEnabled = snapshot.phase != "Starting" && snapshot.phase != "Stopping"; rendering = false
        val time = if (snapshot.lastFix > 0) DateFormat.getDateTimeInstance().format(Date(snapshot.lastFix)) else getString(R.string.location_no_fix)
        controls.status.text = getString(R.string.location_android_status, snapshot.phase, snapshot.fixes, time, if (snapshot.fused) "Google fused + GPS/network" else "GPS/network", if (MockLocationRuntime.authorized(requireContext())) "Granted" else "Choose Sakamoto")
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
        val point = MockLocationStore(requireContext()).load()
        val target = Location("selected").apply { latitude = point.latitude; longitude = point.longitude }
        ui?.observed?.text = String.format(Locale.US, "%.6f, %.6f · ±%.0f m\nMock flag: %s · distance from selected: %.0f m\nFix time: %s", fix.latitude, fix.longitude, fix.accuracy, LocationCompat.isMock(fix), fix.distanceTo(target), DateFormat.getDateTimeInstance().format(Date(fix.time)))
    }
    private fun cancelObservation() {
        observationActive = false; observationEpoch++; handler.removeCallbacksAndMessages(null); signal?.cancel(); signal = null; googleToken?.cancel(); googleToken = null
        listener?.let { runCatching { context?.getSystemService(LocationManager::class.java)?.removeUpdates(it) } }; listener = null
    }
    private fun share() {
        val snapshot = MockLocationRuntime.snapshot.value; val point = MockLocationStore(requireContext()).load()
        val version = runCatching { requireContext().packageManager.getPackageInfo(requireContext().packageName, 0).versionName }.getOrDefault("unknown")
        val report = "Sakamoto $version · Android ${Build.VERSION.RELEASE}\nVirtual location · ${Date()}\nWGS84: ${point.latitude}, ${point.longitude}; accuracy: ${point.accuracy} m\nStatus: ${snapshot.phase}; injected fixes: ${snapshot.fixes}; fused mock: ${snapshot.fused}; last fix: ${snapshot.lastFix}\nDetail: ${snapshot.error}\nObserved: ${ui?.observed?.text}"
        startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, report), getString(R.string.location_share)))
    }
    override fun onDestroyView() { cancelObservation(); ui = null; super.onDestroyView() }
}
