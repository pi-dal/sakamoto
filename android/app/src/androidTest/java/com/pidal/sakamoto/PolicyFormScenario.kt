package com.pidal.sakamoto

import android.app.Instrumentation
import android.view.View
import android.view.ViewGroup
import android.widget.AutoCompleteTextView
import android.widget.EditText
import android.widget.TextView
import com.pidal.sakamoto.ui.AddPolicySheet

object PolicyFormScenario {
    const val domain = "explicit-direct.fixture.example"

    fun addFromRulesPage(test: Instrumentation, activity: MainActivity, host: String) {
        fun views(v: View): Sequence<View> = sequence {
            yield(v)
            if (v is ViewGroup) for (i in 0 until v.childCount) yieldAll(views(v.getChildAt(i)))
        }
        fun row(label: Int) {
            val page = checkNotNull(activity.supportFragmentManager.primaryNavigationFragment).requireView()
            var v: View = views(page).filterIsInstance<TextView>().first { it.text == activity.getString(label) }
            while (!v.isClickable) v = v.parent as View
            check(v.performClick())
            activity.supportFragmentManager.executePendingTransactions()
        }
        test.runOnMainSync {
            activity.openChild(com.pidal.sakamoto.ui.ConfigFragment(), activity.getString(R.string.config_manage_section))
            activity.supportFragmentManager.executePendingTransactions()
            row(R.string.policy_effective_rules)
            row(R.string.add_policy_action)
            val page = checkNotNull(activity.supportFragmentManager.primaryNavigationFragment).requireView()
            views(page).filterIsInstance<EditText>().single().setText(host)
            row(R.string.data_outbound)
        }
        test.waitForIdleSync()
        test.runOnMainSync {
            val dialog = android.view.inspector.WindowInspector.getGlobalWindowViews().asReversed().first { window ->
                views(window).any { it is android.widget.ListView }
            }
            val list = views(dialog).filterIsInstance<android.widget.ListView>().single()
            val pos = (0 until list.count).single { list.getItemAtPosition(it).toString() == "MainProxy" }
            check(list.performItemClick(list.getChildAt(pos - list.firstVisiblePosition), pos, list.getItemIdAtPosition(pos)))
            val toolbar = activity.findViewById<com.google.android.material.appbar.MaterialToolbar>(R.id.top_app_bar)
            check(toolbar.menu.performIdentifierAction(R.id.editor_save, 0))
        }
        test.waitForIdleSync()
    }

    fun add(test: Instrumentation, activity: MainActivity, action: String, host: String = domain) {
        val sheet = AddPolicySheet()
        test.runOnMainSync { sheet.showNow(activity.supportFragmentManager, "test-policy") }
        fun descendants(view: View): Sequence<View> = sequence {
            yield(view)
            if (view is ViewGroup) for (i in 0 until view.childCount) yieldAll(descendants(view.getChildAt(i)))
        }
        test.runOnMainSync {
            val views = descendants(sheet.requireView()).toList()
            views.filterIsInstance<EditText>().single { it !is AutoCompleteTextView }.setText(host)
            views.filterIsInstance<AutoCompleteTextView>().single().setText(action, false)
            check(views.filterIsInstance<TextView>().single { it.text.toString().equals(activity.getString(R.string.add), true) }.performClick())
        }
        test.waitForIdleSync()
        check(!sheet.isAdded)
    }
}
