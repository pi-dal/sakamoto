package com.pidal.sakamoto.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputMethodManager
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import androidx.fragment.app.Fragment
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import com.google.android.material.button.MaterialButton
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.textfield.TextInputEditText
import com.google.android.material.textfield.TextInputLayout
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.LocationMapDraft
import com.pidal.sakamoto.runtime.LocationMapGeometry
import com.pidal.sakamoto.runtime.LocationPlaceSearch
import com.pidal.sakamoto.runtime.MockCoordinates
import com.pidal.sakamoto.runtime.MockLocationRoute
import com.pidal.sakamoto.runtime.MockLocationRuntime
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import org.maplibre.android.MapLibre
import org.maplibre.android.camera.CameraUpdateFactory
import org.maplibre.android.geometry.LatLng
import org.maplibre.android.geometry.LatLngBounds
import org.maplibre.android.maps.MapLibreMap
import org.maplibre.android.maps.MapView
import org.maplibre.android.maps.Style
import org.maplibre.android.style.expressions.Expression.get
import org.maplibre.android.style.layers.CircleLayer
import org.maplibre.android.style.layers.LineLayer
import org.maplibre.android.style.layers.PropertyFactory.*
import org.maplibre.android.style.layers.SymbolLayer
import org.maplibre.android.style.sources.GeoJsonSource
import org.maplibre.geojson.Feature
import org.maplibre.geojson.FeatureCollection
import org.maplibre.geojson.MultiLineString
import org.maplibre.geojson.Point
import java.util.Locale

/** Native map editor with its own draft and lifecycle; the parent receives only explicit commits. */
class LocationMapFragment : Fragment() {
    private lateinit var draft: LocationMapDraft
    private var mapView: MapView? = null
    private var map: MapLibreMap? = null
    private var style: Style? = null
    private var status: TextView? = null
    private var message: TextView? = null
    private var done: MaterialButton? = null
    private var undo: MaterialButton? = null
    private var retry: MaterialButton? = null
    private var query: TextInputEditText? = null
    private var searchButton: MaterialButton? = null
    private var resultsDialog: androidx.appcompat.app.AlertDialog? = null
    private var fitPending = false
    private var searchBusy = false
    private var drawnPoints: List<MockCoordinates>? = null
    private fun dp(value: Int) = (value * resources.displayMetrics.density).toInt()

    override fun onCreate(state: Bundle?) {
        super.onCreate(state)
        val source = state ?: requireArguments()
        val latitudes = source.getDoubleArray("latitudes") ?: doubleArrayOf()
        val longitudes = source.getDoubleArray("longitudes") ?: doubleArrayOf()
        draft = LocationMapDraft(requireArguments().getBoolean("route"), latitudes.indices.map { MockCoordinates(latitudes[it], longitudes[it]) })
    }
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        MapLibre.getInstance(requireContext())
        val root = LinearLayout(requireContext()).apply { orientation = LinearLayout.VERTICAL; setBackgroundColor(context.getColor(R.color.surface)) }
        val searchRow = LinearLayout(requireContext()).apply { orientation = LinearLayout.HORIZONTAL; gravity = android.view.Gravity.CENTER_VERTICAL; setPadding(dp(16), dp(8), dp(16), dp(8)) }
        val input = TextInputLayout(requireContext()).apply { hint = getString(R.string.location_map_search_hint); boxBackgroundMode = TextInputLayout.BOX_BACKGROUND_OUTLINE; endIconMode = TextInputLayout.END_ICON_CLEAR_TEXT }
        query = TextInputEditText(input.context).apply {
            isSingleLine = true; imeOptions = EditorInfo.IME_ACTION_SEARCH
            setText(state?.getString("query") ?: "")
            setOnEditorActionListener { _, action, _ -> if (action == EditorInfo.IME_ACTION_SEARCH) { searchPlaces(); true } else false }
        }
        input.addView(query, LinearLayout.LayoutParams(-1, -2))
        searchRow.addView(input, LinearLayout.LayoutParams(0, -2, 1f))
        searchButton = MaterialButton(requireContext(), null, com.google.android.material.R.attr.borderlessButtonStyle).apply {
            text = getString(R.string.location_map_search); minHeight = dp(48); setOnClickListener { searchPlaces() }
        }
        searchRow.addView(searchButton, LinearLayout.LayoutParams(-2, -2).apply { marginStart = dp(8) }); root.addView(searchRow)
        val frame = FrameLayout(requireContext())
        val view = MapView(requireContext()); mapView = view
        frame.addView(view, FrameLayout.LayoutParams(-1, -1))
        frame.addView(androidx.appcompat.widget.AppCompatImageView(requireContext()).apply {
            setImageResource(R.drawable.ic_map_center)
            imageTintList = android.content.res.ColorStateList.valueOf(context.getColor(R.color.on_surface))
            setBackgroundResource(R.drawable.map_center_background)
            setPadding(dp(4), dp(4), dp(4), dp(4)); importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
        }, FrameLayout.LayoutParams(dp(28), dp(28), android.view.Gravity.CENTER))
        root.addView(frame, LinearLayout.LayoutParams(-1, 0, 1f))
        view.onCreate(state?.getBundle("mapState"))
        view.addOnDidFailLoadingMapListener {
            if (mapView === view) { message?.text = getString(R.string.location_map_load_error); retry?.visibility = View.VISIBLE }
        }
        view.getMapAsync { ready ->
            if (mapView !== view) return@getMapAsync
            map = ready
            ready.uiSettings.isRotateGesturesEnabled = false; ready.uiSettings.isTiltGesturesEnabled = false
            ready.addOnMapClickListener { point -> choose(MockCoordinates(point.latitude, point.longitude)); true }
            if (state?.getBundle("mapState") == null) {
                val center = centerPoint()
                ready.moveCamera(CameraUpdateFactory.newLatLngZoom(LatLng(center.latitude, center.longitude), if (center.latitude == 0.0 && center.longitude == 0.0) 1.5 else 13.0))
                fitPending = draft.points.count() > 1 || draft.points.firstOrNull()?.let { it.latitude != 0.0 || it.longitude != 0.0 } == true
            }
            loadStyle()
        }
        val footerScroll = ScrollView(requireContext())
        val page = GroupedPage(requireContext())
        val footer = LinearLayout(requireContext()).apply { orientation = LinearLayout.VERTICAL }
        status = page.paragraph(footer, "")
        message = page.text(getString(if (draft.isRoute) R.string.location_map_route_hint else R.string.location_map_position_hint)).apply {
            setPadding(dp(20), 0, dp(20), dp(8)); footer.addView(this)
            accessibilityLiveRegion = View.ACCESSIBILITY_LIVE_REGION_POLITE
        }
        val actions = LinearLayout(requireContext()).apply { orientation = LinearLayout.HORIZONTAL; setPadding(dp(4), 0, dp(4), 0) }
        fun action(label: Int, block: () -> Unit): MaterialButton = MaterialButton(requireContext(), null, com.google.android.material.R.attr.borderlessButtonStyle).apply {
            text = getString(label); minHeight = dp(48); setOnClickListener { block() }; actions.addView(this, LinearLayout.LayoutParams(0, -2, 1f))
        }
        action(if (draft.isRoute) R.string.location_map_add_center else R.string.location_map_choose_center) { map?.cameraPosition?.target?.let { choose(MockCoordinates(it.latitude, it.longitude)) } }
        action(R.string.location_map_show_all) { fit() }
        if (draft.isRoute) undo = action(R.string.location_map_undo) { draft.undo(); redraw(); render() }
        footer.addView(actions)
        if (draft.isRoute) page.button(footer, getString(R.string.location_map_clear)) {
            resultsDialog?.dismiss()
            resultsDialog = MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.location_map_clear_confirm)
                .setPositiveButton(R.string.location_map_clear) { _, _ -> draft.clear(); redraw(); render() }.setNegativeButton(android.R.string.cancel, null).show()
        }
        retry = page.button(footer, getString(R.string.location_map_retry)) { loadStyle() }.apply { visibility = View.GONE }
        done = page.button(footer, getString(R.string.location_map_done), primary = true) {
            try {
                val points = draft.commit()
                parentFragmentManager.setFragmentResult(RESULT, Bundle().apply {
                    putBoolean("route", draft.isRoute); putDoubleArray("latitudes", points.map { it.latitude }.toDoubleArray()); putDoubleArray("longitudes", points.map { it.longitude }.toDoubleArray())
                })
                parentFragmentManager.popBackStack()
            } catch (error: Exception) { message?.text = error.message }
        }
        // Attribution controls remain visible on the map. Search attribution is separate.
        page.paragraph(footer, getString(R.string.location_map_search_attribution)).apply { setTextAppearance(com.google.android.material.R.style.TextAppearance_Material3_BodySmall) }
        footerScroll.addView(footer); root.addView(footerScroll, LinearLayout.LayoutParams(-1, -2))
        // Keep a usable map at large font sizes and in landscape; footer actions can scroll.
        root.addOnLayoutChangeListener { _, _, top, _, bottom, _, _, _, _ ->
            val height = bottom - top
            val cap = (height * 0.45).toInt()
            if (cap > 0 && footerScroll.layoutParams.height != cap) { footerScroll.layoutParams = LinearLayout.LayoutParams(-1, cap) }
        }
        render(); return root
    }
    override fun onViewCreated(view: View, state: Bundle?) {
        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(Lifecycle.State.RESUMED) { MockLocationRuntime.snapshot.collect { redraw() } }
        }
    }
    private fun centerPoint(): MockCoordinates = MockCoordinates(requireArguments().getDouble("centerLatitude"), requireArguments().getDouble("centerLongitude"))
    private fun choose(point: MockCoordinates) {
        try { draft.choose(point); message?.text = getString(if (draft.isRoute) R.string.location_map_route_hint else R.string.location_map_position_hint); redraw(); render() }
        catch (error: Exception) { message?.text = error.message }
    }
    private fun render() {
        val points = draft.points
        status?.text = if (draft.isRoute) getString(R.string.location_map_route_count, points.size, points.zipWithNext().sumOf { (a, b) -> MockLocationRoute.distance(a, b) } / 1000)
        else points.firstOrNull()?.let { String.format(Locale.US, "Selected: %.6f, %.6f", it.latitude, it.longitude) } ?: getString(R.string.location_map_position_hint)
        done?.isEnabled = points.size >= if (draft.isRoute) 2 else 1
        undo?.isEnabled = points.isNotEmpty()
    }
    private fun loadStyle() {
        val owner = map ?: return; val view = mapView ?: return
        style = null; drawnPoints = null; retry?.visibility = View.GONE; message?.text = getString(R.string.location_map_loading)
        owner.setStyle(Style.Builder().fromUri("https://tiles.openfreemap.org/styles/liberty")) { loaded ->
            if (mapView !== view) return@setStyle
            style = loaded
            // Liberty is a light basemap even when the app chrome is dark.
            val mapConfiguration = android.content.res.Configuration(resources.configuration).apply {
                uiMode = (uiMode and android.content.res.Configuration.UI_MODE_NIGHT_MASK.inv()) or android.content.res.Configuration.UI_MODE_NIGHT_NO
            }
            val mapContext = requireContext().createConfigurationContext(mapConfiguration)
            val primary = mapContext.getColor(R.color.primary)
            loaded.addSource(GeoJsonSource("sakamoto-stops")); loaded.addSource(GeoJsonSource("sakamoto-path")); loaded.addSource(GeoJsonSource("sakamoto-live"))
            loaded.addLayer(LineLayer("sakamoto-path-line", "sakamoto-path").withProperties(lineColor(primary), lineWidth(4f)))
            loaded.addLayer(CircleLayer("sakamoto-stop-dot", "sakamoto-stops").withProperties(circleColor(primary), circleRadius(12f), circleStrokeWidth(2f), circleStrokeColor(mapContext.getColor(R.color.surface))))
            loaded.addLayer(SymbolLayer("sakamoto-stop-label", "sakamoto-stops").withProperties(textField(get("label")), textFont(arrayOf("Noto Sans Bold")), textSize(13f), textColor(mapContext.getColor(R.color.on_primary)), textAllowOverlap(true), textIgnorePlacement(true)))
            loaded.addLayer(CircleLayer("sakamoto-live-dot", "sakamoto-live").withProperties(circleColor(mapContext.getColor(R.color.on_surface)), circleRadius(7f), circleStrokeWidth(3f), circleStrokeColor(mapContext.getColor(R.color.surface))))
            redraw(); if (fitPending) { fit(); fitPending = false }
            message?.text = getString(if (draft.isRoute) R.string.location_map_route_hint else R.string.location_map_position_hint)
        }
    }
    private fun redraw() {
        val loaded = style ?: return
        val points = draft.points
        if (drawnPoints != points) {
            drawnPoints = points
            val features = points.mapIndexed { index, point -> Feature.fromGeometry(Point.fromLngLat(point.longitude, point.latitude)).apply { addStringProperty("label", if (draft.isRoute) (index + 1).toString() else "") } }
            loaded.getSourceAs<GeoJsonSource>("sakamoto-stops")?.setGeoJson(FeatureCollection.fromFeatures(features))
            val segments = LocationMapGeometry.segments(points).map { leg -> leg.map { Point.fromLngLat(it.longitude, it.latitude) } }
            val path = if (segments.isEmpty()) emptyList() else listOf(Feature.fromGeometry(MultiLineString.fromLngLats(segments)))
            loaded.getSourceAs<GeoJsonSource>("sakamoto-path")?.setGeoJson(FeatureCollection.fromFeatures(path))
        }
        val snapshot = MockLocationRuntime.snapshot.value
        val live = snapshot.point?.takeIf { snapshot.phase == "Active" }?.let { listOf(Feature.fromGeometry(Point.fromLngLat(it.longitude, it.latitude))) } ?: emptyList()
        loaded.getSourceAs<GeoJsonSource>("sakamoto-live")?.setGeoJson(FeatureCollection.fromFeatures(live))
    }
    private fun fit() {
        val points = draft.points
        if (points.isEmpty()) return
        if (points.size == 1) map?.animateCamera(CameraUpdateFactory.newLatLngZoom(LatLng(points[0].latitude, points[0].longitude), 14.0))
        else map?.animateCamera(CameraUpdateFactory.newLatLngBounds(LatLngBounds.Builder().includes(points.map { LatLng(it.latitude, it.longitude) }).build(), dp(48)))
    }
    private fun searchPlaces() {
        val text = query?.text?.toString()?.trim().orEmpty(); if (text.isEmpty() || searchBusy) return
        (requireContext().getSystemService(InputMethodManager::class.java)).hideSoftInputFromWindow(query?.windowToken, 0)
        query?.clearFocus(); query?.isEnabled = false; searchBusy = true; searchButton?.isEnabled = false; message?.text = getString(R.string.location_map_searching)
        viewLifecycleOwner.lifecycleScope.launch {
            try {
                val results = LocationPlaceSearch.search(text)
                if (results.isEmpty()) message?.text = getString(R.string.location_map_no_results)
                else {
                    message?.text = getString(R.string.location_map_choose_result)
                    resultsDialog?.dismiss()
                    resultsDialog = MaterialAlertDialogBuilder(requireContext()).setTitle(R.string.location_map_choose_result)
                        .setItems(results.map { it.name + if (it.detail.isBlank()) "" else "\n${it.detail}" }.toTypedArray()) { _, index ->
                            val point = results[index].point
                            choose(point); map?.animateCamera(CameraUpdateFactory.newLatLngZoom(LatLng(point.latitude, point.longitude), 15.0))
                        }.setNegativeButton(android.R.string.cancel, null).show()
                }
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { message?.text = getString(R.string.location_map_search_error) }
            finally { searchBusy = false; searchButton?.isEnabled = true; query?.isEnabled = true }
        }
    }
    override fun onStart() { super.onStart(); mapView?.onStart() }
    override fun onResume() { super.onResume(); mapView?.onResume() }
    override fun onPause() { mapView?.onPause(); super.onPause() }
    override fun onStop() { mapView?.onStop(); super.onStop() }
    override fun onLowMemory() { super.onLowMemory(); mapView?.onLowMemory() }
    override fun onSaveInstanceState(outState: Bundle) {
        outState.putDoubleArray("latitudes", draft.points.map { it.latitude }.toDoubleArray()); outState.putDoubleArray("longitudes", draft.points.map { it.longitude }.toDoubleArray())
        outState.putString("query", query?.text?.toString())
        val mapState = Bundle(); mapView?.onSaveInstanceState(mapState); outState.putBundle("mapState", mapState)
        super.onSaveInstanceState(outState)
    }
    override fun onDestroyView() {
        resultsDialog?.dismiss(); resultsDialog = null
        mapView?.onDestroy(); mapView = null; map = null; style = null; drawnPoints = null
        status = null; message = null; done = null; undo = null; retry = null; query = null; searchButton = null
        super.onDestroyView()
    }
    companion object {
        const val RESULT = "location-map-selection"
        fun create(route: Boolean, points: List<MockCoordinates>, center: MockCoordinates) = LocationMapFragment().apply {
            arguments = Bundle().apply {
                putBoolean("route", route); putDoubleArray("latitudes", points.map { it.latitude }.toDoubleArray()); putDoubleArray("longitudes", points.map { it.longitude }.toDoubleArray())
                putDouble("centerLatitude", center.latitude); putDouble("centerLongitude", center.longitude)
            }
        }
    }
}
