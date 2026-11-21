package com.pidal.sakamoto

import android.app.Instrumentation
import android.view.View
import android.view.inspector.WindowInspector
import android.widget.ListView
import androidx.core.view.descendants
import com.pidal.sakamoto.ui.ExperimentsFragment

internal object UpgradeConsentScenario {
    fun confirmAuto(test: Instrumentation, activity: MainActivity) = choose(test, activity, R.string.experiment_mode, "auto")
    fun enableFallback(test: Instrumentation, activity: MainActivity) = choose(test, activity, R.string.experiment_fallback, "on")
    fun chooseMode(test: Instrumentation, activity: MainActivity, mode: String) = choose(test, activity, R.string.experiment_mode, mode)

    private fun choose(test: Instrumentation, activity: MainActivity, label: Int, value: String) {
        val setting = activity.getString(label)
        val screen = ExperimentsFragment()
        test.runOnMainSync {
            activity.openChild(screen, activity.getString(R.string.experiments_title))
            activity.supportFragmentManager.executePendingTransactions()
            val matches = ArrayList<View>()
            screen.requireView().findViewsWithText(matches, setting, View.FIND_VIEWS_WITH_TEXT)
            var row: View = matches.filterIsInstance<android.widget.TextView>().single { it.text == setting }
            while (!row.isClickable) row = row.parent as View
            check(row.performClick())
        }
        test.waitForIdleSync()
        test.runOnMainSync {
            val list = WindowInspector.getGlobalWindowViews().filterIsInstance<android.view.ViewGroup>()
                .flatMap { it.descendants.filterIsInstance<ListView>().toList() }.single()
            val selected = (0 until list.count).single { list.getItemAtPosition(it) == value }
            check(list.performItemClick(list.getChildAt(selected - list.firstVisiblePosition), selected, list.getItemIdAtPosition(selected)))
        }
        test.waitForIdleSync()
        if (value != "auto") return
        test.runOnMainSync {
            val confirm = WindowInspector.getGlobalWindowViews().mapNotNull { it.findViewById<android.widget.Button>(android.R.id.button1) }
                .single { it.isShown && it.text.toString().equals(activity.getString(R.string.s3_enable_action), true) }
            check(confirm.performClick())
        }
        test.waitForIdleSync()
    }
}
