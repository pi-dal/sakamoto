package com.pidal.sakamoto

import android.app.Instrumentation
import android.view.View
import android.view.ViewGroup
import android.view.inspector.WindowInspector
import android.widget.ListView
import android.widget.TextView
import androidx.fragment.app.Fragment
import com.pidal.sakamoto.ui.ExperimentsFragment
import com.pidal.sakamoto.ui.RoutingPolicyFragment

class RoutingLifecycleForm(private val test: Instrumentation, private val activity: MainActivity) {
    private lateinit var page: Fragment
    private fun views(v: View): Sequence<View> = sequence {
        yield(v)
        if (v is ViewGroup) for (i in 0 until v.childCount) yieldAll(views(v.getChildAt(i)))
    }
    private fun open(fragment: Fragment) {
        page = fragment
        test.runOnMainSync {
            activity.openChild(fragment, activity.getString(R.string.policy_effective_rules))
            activity.supportFragmentManager.executePendingTransactions()
        }
        test.waitForIdleSync()
    }
    private fun row(label: String, long: Boolean = false) {
        test.runOnMainSync {
            var view: View = views(page.requireView()).filterIsInstance<TextView>().first { it.text == label }
            while (if (long) !view.isLongClickable else !view.isClickable) view = view.parent as View
            check(if (long) view.performLongClick() else view.performClick())
        }
        test.waitForIdleSync()
    }
    private fun choose(label: Int, value: String) {
        row(activity.getString(label))
        test.runOnMainSync {
            val list = WindowInspector.getGlobalWindowViews().asReversed().asSequence().flatMap { views(it) }.filterIsInstance<ListView>().first()
            val i = (0 until list.count).single { list.getItemAtPosition(it).toString() == value }
            list.performItemClick(list.getChildAt(i - list.firstVisiblePosition), i, list.getItemIdAtPosition(i))
        }
        test.waitForIdleSync()
    }
    fun finalRoundTrip() {
        open(RoutingPolicyFragment()); choose(R.string.policy_final, "MainProxy")
        open(ExperimentsFragment()); choose(R.string.experiment_mode, "on"); choose(R.string.experiment_mode, "off")
    }
    private fun move(label: String, direction: Int) {
        row(label, true)
        test.runOnMainSync {
            val list = WindowInspector.getGlobalWindowViews().asReversed().asSequence().flatMap { views(it) }.filterIsInstance<ListView>().first()
            val i = (0 until list.count).single { list.getItemAtPosition(it).toString() == activity.getString(direction) }
            list.performItemClick(list.getChildAt(i - list.firstVisiblePosition), i, list.getItemIdAtPosition(i))
        }
        test.waitForIdleSync()
    }
    fun rejectBeforeLearning(host: String) {
        PolicyFormScenario.add(test, activity, "reject", host)
        open(RoutingPolicyFragment())
        move("rule_set: rs-proxy", R.string.rule_move_up)
        val reject = "domain: $host"
        fun rules() = com.pidal.sakamoto.runtime.RoutingSnapshot.parse(com.pidal.sakamoto.runtime.ConfigRepository.load(activity).generatedContent).rules
        val distance = rules().indexOfFirst { it.match == "rule_set: rs-proxy" } - rules().indexOfFirst { it.match == reject }
        check(distance > 0)
        repeat(distance) { move(reject, R.string.rule_move_down) }
        val labels = rules().map { it.match }
        check(labels.windowed(3).any { it == listOf("rule_set: rs-proxy", reject, "rule_set: sakamoto-auto-proxy") })
    }
    fun setLearning(enabled: Boolean) {
        open(ExperimentsFragment())
        choose(R.string.experiment_mode, if (enabled) "auto" else "off")
        if (enabled) {
            test.runOnMainSync {
                val button = WindowInspector.getGlobalWindowViews().last().findViewById<View>(android.R.id.button1)
                check(button.performClick())
            }
            test.waitForIdleSync()
        }
    }
    fun moveAndForget(host: String) {
        open(RoutingPolicyFragment()); move("rule_set: rs-proxy", R.string.rule_move_up)
        forget(host)
        choose(R.string.experiment_mode, "off")
    }
    fun forget(host: String) {
        open(ExperimentsFragment()); row(host)
        test.runOnMainSync { WindowInspector.getGlobalWindowViews().last { it.findViewById<View>(android.R.id.button1) != null }.findViewById<View>(android.R.id.button1).performClick() }
        test.waitForIdleSync()
    }
}
