package com.pidal.sakamoto

import android.app.Instrumentation
import android.content.Intent
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.core.widget.NestedScrollView
import com.google.android.material.button.MaterialButton
import com.pidal.sakamoto.runtime.MockLocationRuntime
import com.pidal.sakamoto.ui.LocationMapFragment
import com.pidal.sakamoto.ui.VirtualLocationFragment
import java.io.File

/** Emulator lane: permission-only setup must not start a service; real guide destinations round-trip. */
class LocationSetupSmokeChecks(private val instrumentation: Instrumentation) {
    fun run() {
        val context = instrumentation.targetContext
        context.getSharedPreferences("virtual-location", 0).edit().putBoolean("setupGuideExpanded", true).commit()
        check(MockLocationRuntime.snapshot.value.phase == "Off")
        val activity = instrumentation.startActivitySync(Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        fun descendants(view: View): List<View> = listOf(view) + if (view is ViewGroup) (0 until view.childCount).flatMap { descendants(view.getChildAt(it)) } else emptyList()
        fun views() = descendants(activity.supportFragmentManager.primaryNavigationFragment!!.requireView())
        fun click(label: Int) = instrumentation.runOnMainSync {
            views().filterIsInstance<MaterialButton>().first { it.text.toString() == context.getString(label) && it.isShown }.performClick()
            activity.supportFragmentManager.executePendingTransactions()
        }
        instrumentation.runOnMainSync {
            activity.openChild(VirtualLocationFragment(), context.getString(R.string.virtual_location))
            activity.supportFragmentManager.executePendingTransactions()
        }
        instrumentation.waitForIdleSync()
        Thread.sleep(500)
        instrumentation.runOnMainSync {
            check(views().filterIsInstance<TextView>().any { it.text.toString() == context.getString(R.string.location_setup_mock_title) && it.isShown }) { "Setup steps are not visible" }
        }
        val bitmap = checkNotNull(instrumentation.uiAutomation.takeScreenshot())
        File(context.cacheDir, "location-setup.png").outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
        bitmap.recycle()
        click(R.string.location_setup_app_settings)
        Thread.sleep(700)
        check(instrumentation.uiAutomation.rootInActiveWindow?.packageName?.toString() == "com.android.settings") { "App settings jump did not open Android Settings" }
        instrumentation.sendKeyDownUpSync(android.view.KeyEvent.KEYCODE_BACK)
        instrumentation.waitForIdleSync()
        Thread.sleep(500)
        instrumentation.runOnMainSync {
            check(views().filterIsInstance<TextView>().any { it.text.toString() == context.getString(R.string.location_setup_permission_ready) }) { "Permission state was not refreshed on return" }
        }
        // The runner grants permission and mock authorization beforehand so the old
        // permission callback would really start a service. The guide must only grant/check access.
        click(R.string.location_setup_allow)
        Thread.sleep(700)
        check(MockLocationRuntime.snapshot.value.phase == "Off") { "Granting access from the guide started simulation" }
        click(R.string.location_map_choose)
        instrumentation.runOnMainSync {
            check(activity.supportFragmentManager.primaryNavigationFragment is LocationMapFragment)
            activity.supportFragmentManager.popBackStackImmediate()
            check(activity.supportFragmentManager.primaryNavigationFragment is VirtualLocationFragment)
        }
        click(R.string.location_setup_route)
        Thread.sleep(700)
        instrumentation.runOnMainSync {
            val root = activity.supportFragmentManager.primaryNavigationFragment!!.requireView() as NestedScrollView
            check(root.scrollY > 0) { "Moving route jump did not scroll" }
            val button = views().filterIsInstance<MaterialButton>().first { it.text.toString() == context.getString(R.string.location_map_edit_route) }
            check(button.getGlobalVisibleRect(android.graphics.Rect())) { "Moving route editor is not visible after jump" }
            check(!views().filterIsInstance<TextView>().first { it.text.toString() == context.getString(R.string.location_setup_mock_title) }.isShown) { "Guide did not collapse" }
            root.scrollTo(0, 0)
        }
        click(R.string.location_setup_show)
        instrumentation.runOnMainSync {
            check(views().filterIsInstance<TextView>().any { it.text.toString() == context.getString(R.string.location_setup_mock_title) && it.isShown })
        }
        check(MockLocationRuntime.snapshot.value.phase == "Off")
    }
}
