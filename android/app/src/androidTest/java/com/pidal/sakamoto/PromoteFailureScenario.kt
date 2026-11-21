package com.pidal.sakamoto

import android.app.Instrumentation
import android.view.View
import android.view.ViewGroup
import android.view.inspector.WindowInspector
import android.widget.ListView
import android.widget.TextView
import com.pidal.sakamoto.ui.ExperimentsFragment

/** Promote a verified domain using the experiment page, then remove its device learning record. */
object PromoteFailureScenario {
    fun run(test: Instrumentation, activity: MainActivity, host: String, onSaved: () -> Unit) {
        val page = ExperimentsFragment()
        fun views(view: View): Sequence<View> = sequence {
            yield(view)
            if (view is ViewGroup) for (i in 0 until view.childCount) yieldAll(views(view.getChildAt(i)))
        }
        fun clickRow(text: String) {
            var view: View = views(page.requireView()).filterIsInstance<TextView>().first { it.text == text }
            while (!view.isClickable) view = view.parent as View
            check(view.performClick())
        }
        fun dialog() = WindowInspector.getGlobalWindowViews().last { it.findViewById<View>(android.R.id.button1) != null }
        test.runOnMainSync {
            activity.openChild(page, activity.getString(R.string.experiments_title))
            activity.supportFragmentManager.executePendingTransactions()
            clickRow(activity.getString(R.string.experiment_add_failures))
        }
        test.waitForIdleSync()
        test.runOnMainSync {
            val window = dialog()
            val list = views(window).filterIsInstance<ListView>().single()
            val pos = (0 until list.count).single { list.getItemAtPosition(it).toString() == host }
            list.performItemClick(list.getChildAt(pos - list.firstVisiblePosition), pos, list.getItemIdAtPosition(pos))
            check(list.isItemChecked(pos))
            window.findViewById<View>(android.R.id.button1).performClick()
        }
        test.waitForIdleSync()
        onSaved()
        test.runOnMainSync { clickRow(host) }
        test.waitForIdleSync()
        test.runOnMainSync { dialog().findViewById<View>(android.R.id.button1).performClick() }
        test.waitForIdleSync()
    }
}
