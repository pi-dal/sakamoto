package com.pidal.sakamoto

import android.os.Bundle
import androidx.appcompat.app.AppCompatActivity
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.fragment.app.Fragment
import androidx.lifecycle.Lifecycle
import com.pidal.sakamoto.databinding.ActivityMainBinding
import com.pidal.sakamoto.ui.ConfigFragment
import com.pidal.sakamoto.ui.DataFragment
import com.pidal.sakamoto.ui.HomeFragment
import com.pidal.sakamoto.ui.SettingsFragment

/** Four retained top-level destinations, with a real child-page back stack. */
class MainActivity : AppCompatActivity() {
    private lateinit var binding: ActivityMainBinding
    private var selectedTab = R.id.nav_home

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        WindowCompat.setDecorFitsSystemWindows(window, false)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)
        ViewCompat.setOnApplyWindowInsetsListener(binding.root) { view, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout())
            val keyboard = insets.getInsets(WindowInsetsCompat.Type.ime())
            view.setPadding(bars.left, bars.top, bars.right, maxOf(bars.bottom, keyboard.bottom))
            WindowInsetsCompat.CONSUMED
        }
        ViewCompat.requestApplyInsets(binding.root)
        selectedTab = savedInstanceState?.getInt("selectedTab", R.id.nav_home) ?: R.id.nav_home
        binding.topAppBar.setNavigationOnClickListener { onBackPressedDispatcher.onBackPressed() }
        binding.bottomNav.setOnItemSelectedListener {
            val editor = supportFragmentManager.primaryNavigationFragment as? com.pidal.sakamoto.ui.EditorFragment
            if (editor != null && editor.hasUnsavedChanges()) {
                editor.confirmLeaving { binding.bottomNav.selectedItemId = it.itemId }
                false
            } else { selectTab(it.itemId); true }
        }
        binding.bottomNav.setOnItemReselectedListener {
            val editor = supportFragmentManager.primaryNavigationFragment as? com.pidal.sakamoto.ui.EditorFragment
            if (editor != null && editor.hasUnsavedChanges()) editor.confirmLeaving {
                supportFragmentManager.popBackStackImmediate(null, androidx.fragment.app.FragmentManager.POP_BACK_STACK_INCLUSIVE)
            } else supportFragmentManager.popBackStackImmediate(null, androidx.fragment.app.FragmentManager.POP_BACK_STACK_INCLUSIVE)
        }
        supportFragmentManager.addOnBackStackChangedListener { updateChrome() }
        if (savedInstanceState == null) selectTab(selectedTab) else updateChrome()
        handleSurfaceAction(intent)
    }

    override fun onNewIntent(intent: android.content.Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleSurfaceAction(intent)
    }

    private fun handleSurfaceAction(intent: android.content.Intent) {
        val action = intent.action
        intent.action = null
        when (action) {
            com.pidal.sakamoto.runtime.SystemStatusSurface.ACTION_CONNECT -> {
                selectTab(R.id.nav_home)
                (supportFragmentManager.findFragmentByTag("tab-${R.id.nav_home}") as? HomeFragment)?.requestConnect()
            }
            com.pidal.sakamoto.runtime.SystemStatusSurface.ACTION_STATUS -> openChild(com.pidal.sakamoto.ui.VpnStatusFragment(), getString(R.string.vpn_status_title))
        }
    }

    private fun selectTab(id: Int) {
        val manager = supportFragmentManager
        if (manager.isStateSaved) return
        manager.popBackStackImmediate(null, androidx.fragment.app.FragmentManager.POP_BACK_STACK_INCLUSIVE)
        selectedTab = id
        val tag = "tab-$id"
        val target = manager.findFragmentByTag(tag) ?: when (id) {
            R.id.nav_config -> ConfigFragment()
            R.id.nav_data -> DataFragment()
            R.id.nav_settings -> SettingsFragment()
            else -> HomeFragment()
        }
        val transaction = manager.beginTransaction()
        manager.fragments.filter { it.isAdded && it != target && it !is androidx.fragment.app.DialogFragment }.forEach {
            transaction.hide(it).setMaxLifecycle(it, Lifecycle.State.STARTED)
        }
        if (!target.isAdded) transaction.add(R.id.fragment_container, target, tag) else transaction.show(target)
        transaction.setMaxLifecycle(target, Lifecycle.State.RESUMED).setPrimaryNavigationFragment(target).commitNow()
        updateChrome()
    }

    fun openChild(fragment: Fragment, title: String) {
        val manager = supportFragmentManager
        if (manager.isStateSaved) return
        val transaction = manager.beginTransaction()
        manager.fragments.filter { it.isAdded && !it.isHidden && it !is androidx.fragment.app.DialogFragment }.forEach {
            transaction.hide(it).setMaxLifecycle(it, Lifecycle.State.STARTED)
        }
        transaction.add(R.id.fragment_container, fragment).setPrimaryNavigationFragment(fragment)
            .addToBackStack(title).commit()
    }

    fun addToolbarMenu(provider: androidx.core.view.MenuProvider, owner: androidx.lifecycle.LifecycleOwner) {
        binding.topAppBar.addMenuProvider(provider, owner, Lifecycle.State.RESUMED)
    }

    private fun updateChrome() {
        val manager = supportFragmentManager
        val child = manager.backStackEntryCount > 0
        binding.topAppBar.title = if (child) manager.getBackStackEntryAt(manager.backStackEntryCount - 1).name else when (selectedTab) {
            R.id.nav_config -> getString(R.string.tab_config)
            R.id.nav_data -> getString(R.string.tab_data)
            R.id.nav_settings -> getString(R.string.tab_settings)
            else -> getString(R.string.tab_home)
        }
        binding.topAppBar.navigationIcon = if (child) androidx.appcompat.content.res.AppCompatResources.getDrawable(this, androidx.appcompat.R.drawable.abc_ic_ab_back_material) else null
        binding.topAppBar.navigationContentDescription = getString(R.string.navigate_back)
    }

    override fun onSaveInstanceState(outState: Bundle) {
        outState.putInt("selectedTab", selectedTab)
        super.onSaveInstanceState(outState)
    }
}
