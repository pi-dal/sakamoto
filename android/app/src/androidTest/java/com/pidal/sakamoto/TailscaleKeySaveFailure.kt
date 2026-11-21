package com.pidal.sakamoto

import android.app.Instrumentation
import android.system.Os
import android.view.View
import android.view.ViewGroup
import android.view.inspector.WindowInspector
import android.widget.EditText
import android.widget.TextView
import androidx.core.view.descendants
import com.google.android.material.textfield.TextInputLayout
import com.pidal.sakamoto.ui.TailscaleFragment

/** Exercise the real password dialog while the app cannot stage a replacement. */
internal object TailscaleKeySaveFailure {
    fun deleteAndRetry(test: Instrumentation, activity: MainActivity) {
        val file = activity.getFileStreamPath("tailscale-authkey.bin")
        val original = file.readBytes()
        test.runOnMainSync {
            val screen = TailscaleFragment()
            activity.openChild(screen, activity.getString(R.string.ts_authkey_title))
            activity.supportFragmentManager.executePendingTransactions()
            val matches = arrayListOf<View>()
            screen.requireView().findViewsWithText(matches, activity.getString(R.string.ts_authkey_delete), View.FIND_VIEWS_WITH_TEXT)
            check(matches.filterIsInstance<android.widget.Button>().single().performClick())
        }
        test.waitForIdleSync()
        test.runOnMainSync {
            val dialog = WindowInspector.getGlobalWindowViews().single { it.findViewById<View>(android.R.id.button1)?.isShown == true }
            val confirm = dialog.findViewById<View>(android.R.id.button1)
            val dir = activity.filesDir
            val permissions = Os.stat(dir.path).st_mode and 511
            Os.chmod(dir.path, 365)
            try {
                check(confirm.performClick())
                check(dialog.findViewById<TextView>(android.R.id.message).text == activity.getString(R.string.ts_authkey_delete_failed))
                check(file.readBytes().contentEquals(original))
            } finally { Os.chmod(dir.path, permissions) }
            check(confirm.performClick())
            check(!file.exists())
        }
    }

    fun run(test: Instrumentation, activity: MainActivity) {
        val dir = activity.filesDir
        val original = dir.resolve("tailscale-authkey.bin").readBytes()
        test.runOnMainSync {
            val screen = TailscaleFragment()
            val title = activity.getString(R.string.ts_authkey_title)
            activity.openChild(screen, title)
            activity.supportFragmentManager.executePendingTransactions()
            val matches = arrayListOf<View>()
            screen.requireView().findViewsWithText(matches, title, View.FIND_VIEWS_WITH_TEXT)
            var row: View = matches.filterIsInstance<TextView>().single { it.text == title }
            while (!row.isClickable) row = row.parent as View
            check(row.performClick())
        }
        test.waitForIdleSync()
        test.runOnMainSync {
            val dialog = WindowInspector.getGlobalWindowViews().filterIsInstance<ViewGroup>().single { root ->
                root.descendants.any { it is TextInputLayout && it.hint == activity.getString(R.string.ts_authkey_store) }
            }
            val input = dialog.descendants.filterIsInstance<EditText>().single()
            input.setText("fixture-replacement")
            val permissions = Os.stat(dir.path).st_mode and 511
            Os.chmod(dir.path, 365) // 0555. The original file remains writable.
            try {
                check(dialog.findViewById<View>(android.R.id.button1).performClick())
                check(dialog.descendants.filterIsInstance<TextInputLayout>().single().error != null)
            } finally { Os.chmod(dir.path, permissions) }
            check(dialog.findViewById<View>(android.R.id.button2).performClick())
        }
        check(dir.resolve("tailscale-authkey.bin").readBytes().contentEquals(original))
    }
}
