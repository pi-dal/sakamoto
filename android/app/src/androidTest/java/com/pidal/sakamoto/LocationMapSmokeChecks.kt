package com.pidal.sakamoto

import android.app.Instrumentation
import android.content.Intent
import android.os.SystemClock
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import com.google.android.material.button.MaterialButton
import com.pidal.sakamoto.runtime.MockCoordinates
import com.pidal.sakamoto.runtime.MockLocationRoute
import com.pidal.sakamoto.runtime.MockLocationStore
import com.pidal.sakamoto.ui.LocationMapFragment
import com.pidal.sakamoto.ui.VirtualLocationFragment
import org.maplibre.android.maps.MapView
import java.io.File

/** Real GL rendering and draft round-trip without granting location/VPN permissions. Synthetic Cupertino fixture. */
class LocationMapSmokeChecks(private val instrumentation: Instrumentation) {
    fun run() {
        val context = instrumentation.targetContext
        val store = MockLocationStore(context)
        val points = listOf(MockCoordinates(37.3349, -122.009), MockCoordinates(37.333, -122.007), MockCoordinates(37.331, -122.006))
        store.save(points.first()); store.saveRoute(MockLocationRoute(points, 300))
        val activity = instrumentation.startActivitySync(Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        fun descendants(view: View): List<View> = listOf(view) + if (view is ViewGroup) (0 until view.childCount).flatMap { descendants(view.getChildAt(it)) } else emptyList()
        fun views() = descendants(activity.window.decorView)
        fun click(label: Int) = instrumentation.runOnMainSync {
            views().filterIsInstance<MaterialButton>().first { it.text.toString() == context.getString(label) }.performClick()
            activity.supportFragmentManager.executePendingTransactions()
        }
        instrumentation.runOnMainSync { activity.openChild(VirtualLocationFragment(), context.getString(R.string.virtual_location)); activity.supportFragmentManager.executePendingTransactions() }
        click(R.string.location_map_edit_route)
        var ready = false
        val deadline = SystemClock.elapsedRealtime() + 30000
        while (!ready && SystemClock.elapsedRealtime() < deadline) {
            instrumentation.runOnMainSync { views().filterIsInstance<MapView>().single().getMapAsync { ready = it.style?.isFullyLoaded == true } }
            if (!ready) Thread.sleep(200)
        }
        check(ready) { "Map style never loaded" }
        Thread.sleep(3500)
        val bitmap = checkNotNull(instrumentation.uiAutomation.takeScreenshot())
        File(context.cacheDir, "location-map.png").outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }; bitmap.recycle()
        click(R.string.location_map_add_center)
        instrumentation.runOnMainSync { check(views().filterIsInstance<TextView>().any { it.text.startsWith("4 / 32") }) { "Map center did not append a route stop" } }
        click(R.string.location_map_undo)
        instrumentation.runOnMainSync { check(views().filterIsInstance<TextView>().any { it.text.startsWith("3 / 32") }) { "Undo did not restore the route draft" } }
        click(R.string.location_map_done)
        instrumentation.runOnMainSync {
            check(activity.supportFragmentManager.primaryNavigationFragment is VirtualLocationFragment)
            check(store.loadRoute()?.points == points) { "Map commit changed route coordinates" }
        }
        click(R.string.location_map_choose)
        instrumentation.runOnMainSync {
            check(activity.supportFragmentManager.primaryNavigationFragment is LocationMapFragment)
            activity.supportFragmentManager.popBackStackImmediate()
            check(store.load() == points.first()) { "Cancel changed the selected position" }
        }
    }
}
