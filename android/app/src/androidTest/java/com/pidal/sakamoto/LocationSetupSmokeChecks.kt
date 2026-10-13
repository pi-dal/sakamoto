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

/** Emulator lane: one page at a time, destinations round-trip, navigation never starts a service. */
class LocationSetupSmokeChecks(private val instrumentation: Instrumentation) {
    fun run() {
        val context = instrumentation.targetContext
        context.getSharedPreferences("virtual-location", 0).edit().putBoolean("setupWizardSeen", false).putInt("setupWizardStep", 0).commit()
        check(MockLocationRuntime.snapshot.value.phase == "Off")
        val activity = instrumentation.startActivitySync(Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        fun descendants(view: View): List<View> = listOf(view) + if (view is ViewGroup) (0 until view.childCount).flatMap { descendants(view.getChildAt(it)) } else emptyList()
        fun views() = descendants(activity.supportFragmentManager.primaryNavigationFragment!!.requireView())
        fun click(label: Int) = instrumentation.runOnMainSync {
            views().filterIsInstance<MaterialButton>().first { it.text.toString() == context.getString(label) && it.isShown }.performClick()
            activity.supportFragmentManager.executePendingTransactions()
        }
        val titles = intArrayOf(R.string.location_setup_developer_title, R.string.location_setup_mock_title,
            R.string.location_setup_permission_title, R.string.location_setup_position_title,
            R.string.location_setup_start_title, R.string.location_setup_verify_title)
        fun assertStep(index: Int) {
            var shown: List<String> = emptyList()
            var footerVisible = false
            for (attempt in 0 until 40) {
                instrumentation.runOnMainSync {
                    shown = views().filterIsInstance<TextView>().filter { it.isShown }.map { it.text.toString() }
                    val footer = views().filterIsInstance<MaterialButton>().firstOrNull { it.isShown && it.text.toString() == context.getString(if (index == 5) R.string.location_setup_done else R.string.location_setup_next) }
                    val visible = android.graphics.Rect()
                    footerVisible = footer != null && footer.getGlobalVisibleRect(visible) && visible.height() == footer.height
                }
                if (shown.contains(context.getString(titles[index])) && footerVisible) break
                Thread.sleep(100)
            }
            check(shown.contains(context.getString(titles[index]))) { "Expected page ${index + 1}, got $shown" }
            check(titles.filter { it != titles[index] }.none { shown.contains(context.getString(it)) }) { "Multiple setup pages visible" }
            check(footerVisible) { "Footer clipped at page ${index + 1}" }
            check(MockLocationRuntime.snapshot.value.phase == "Off") { "Guide navigation started simulation" }
        }
        fun capture(name: String) {
            instrumentation.uiAutomation.waitForIdle(500, 5000)
            instrumentation.waitForIdleSync()
            // Accessibility/layout can be ready before the surface finishes its
            // system transition or commits the newly selected wizard page.
            Thread.sleep(100)
            check(instrumentation.uiAutomation.rootInActiveWindow?.packageName?.toString() == context.packageName) { "Capture was attempted outside Sakamoto" }
            val bitmap = checkNotNull(instrumentation.uiAutomation.takeScreenshot())
            File(context.cacheDir, name).outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
            bitmap.recycle()
        }
        fun settingsRoundTrip(button: Int, step: Int) {
            click(button)
            var activePackage: String? = null
            for (attempt in 0 until 50) {
                activePackage = instrumentation.uiAutomation.rootInActiveWindow?.packageName?.toString()
                if (activePackage == "com.android.settings") break
                Thread.sleep(100)
            }
            check(activePackage == "com.android.settings") { "Settings shortcut did not open Android Settings (active package: $activePackage)" }
            instrumentation.uiAutomation.waitForIdle(250, 5000)
            instrumentation.sendKeyDownUpSync(android.view.KeyEvent.KEYCODE_BACK)
            for (attempt in 0 until 50) {
                activePackage = instrumentation.uiAutomation.rootInActiveWindow?.packageName?.toString()
                if (activePackage == context.packageName) break
                if (attempt == 25 && activePackage == "com.android.settings") instrumentation.sendKeyDownUpSync(android.view.KeyEvent.KEYCODE_BACK)
                Thread.sleep(100)
            }
            check(activePackage == context.packageName) { "Did not return from Settings (active package: $activePackage)" }
            instrumentation.waitForIdleSync()
            assertStep(step)
        }
        instrumentation.runOnMainSync {
            activity.openChild(VirtualLocationFragment(), context.getString(R.string.virtual_location))
            activity.supportFragmentManager.executePendingTransactions()
        }
        instrumentation.waitForIdleSync(); Thread.sleep(500)
        assertStep(0); capture("location-setup.png")
        click(R.string.location_setup_next); assertStep(1)
        settingsRoundTrip(R.string.location_developer_options, 1)
        click(R.string.location_setup_next); assertStep(2); capture("location-setup-permission.png")
        settingsRoundTrip(R.string.location_setup_app_settings, 2)
        instrumentation.runOnMainSync {
            check(views().filterIsInstance<TextView>().any { it.isShown && it.text.toString() == context.getString(R.string.location_setup_permission_ready) }) { "Permission status did not refresh" }
        }
        // Authorization and permission are already granted: a mistaken start callback would start a service.
        click(R.string.location_setup_allow); Thread.sleep(700)
        check(MockLocationRuntime.snapshot.value.phase == "Off") { "Granting permission started simulation" }
        fun setLocationEnabled(enabled: Boolean) {
            android.os.ParcelFileDescriptor.AutoCloseInputStream(instrumentation.uiAutomation.executeShellCommand("cmd location set-location-enabled $enabled"))
                .bufferedReader().use { it.readText() }
        }
        val locationWasEnabled = androidx.core.location.LocationManagerCompat.isLocationEnabled(context.getSystemService(android.location.LocationManager::class.java))
        try {
            setLocationEnabled(false)
            settingsRoundTrip(R.string.location_setup_app_settings, 2)
            instrumentation.runOnMainSync {
                check(views().filterIsInstance<TextView>().any { it.isShown && it.text.toString() == context.getString(R.string.location_setup_services_needed) }) { "Global Location off was not detected" }
            }
            click(R.string.location_setup_next); click(R.string.location_setup_next); assertStep(4)
            instrumentation.runOnMainSync {
                check(!views().filterIsInstance<MaterialButton>().first { it.isShown && it.text.toString() == context.getString(R.string.location_setup_start) }.isEnabled) { "Start remained enabled while Location was off" }
            }
            capture("location-setup-disabled.png")
            click(R.string.location_setup_review_required); assertStep(2)
        } finally { setLocationEnabled(locationWasEnabled) }
        settingsRoundTrip(R.string.location_setup_app_settings, 2)
        click(R.string.location_setup_next); assertStep(3)
        click(R.string.location_map_choose)
        instrumentation.runOnMainSync { check(activity.supportFragmentManager.primaryNavigationFragment is LocationMapFragment) }
        instrumentation.sendKeyDownUpSync(android.view.KeyEvent.KEYCODE_BACK)
        instrumentation.waitForIdleSync(); Thread.sleep(500)
        assertStep(3)
        click(R.string.location_setup_previous); assertStep(2)
        click(R.string.location_setup_next); click(R.string.location_setup_next); assertStep(4)
        click(R.string.location_setup_route); Thread.sleep(700)
        instrumentation.runOnMainSync {
            val scroll = views().filterIsInstance<NestedScrollView>().first { it.isShown }
            check(scroll.scrollY > 0) { "Route shortcut did not scroll" }
            val button = views().filterIsInstance<MaterialButton>().first { it.text.toString() == context.getString(R.string.location_map_edit_route) }
            check(button.getGlobalVisibleRect(android.graphics.Rect())) { "Route editor not visible after shortcut" }
            scroll.scrollTo(0, 0)
        }
        click(R.string.location_setup_show); assertStep(4)
        instrumentation.runOnMainSync {
            views().filterIsInstance<MaterialButton>().first { it.isShown && it.contentDescription?.toString()?.startsWith(context.getString(R.string.location_setup_jump)) == true }.performClick()
        }
        instrumentation.waitForIdleSync(); Thread.sleep(300)
        val menuText = "6. ${context.getString(R.string.location_setup_verify_title)}"
        var node: android.view.accessibility.AccessibilityNodeInfo? = checkNotNull(instrumentation.uiAutomation.rootInActiveWindow).findAccessibilityNodeInfosByText(menuText).first { it.text?.toString() == menuText }
        while (node != null && !node.isClickable) node = node.parent
        check(node?.performAction(android.view.accessibility.AccessibilityNodeInfo.ACTION_CLICK) == true) { "Jump menu could not select a page" }
        instrumentation.waitForIdleSync(); Thread.sleep(300)
        assertStep(5); capture("location-setup-verify.png")
        click(R.string.location_setup_done)
        click(R.string.location_setup_show); assertStep(5)
        instrumentation.sendKeyDownUpSync(android.view.KeyEvent.KEYCODE_BACK)
        instrumentation.waitForIdleSync(); Thread.sleep(300)
        assertStep(4)
        click(R.string.location_setup_close)
        check(MockLocationRuntime.snapshot.value.phase == "Off")
    }
}
