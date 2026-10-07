package com.pidal.sakamoto

import android.os.Bundle
import android.view.View
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

/** Four retained top-level destinations with a real child-page back stack.
 * Telegram Android shell: surface app bar and floating compact
 * bottom tabs that only exist on top-level pages (child pages take the full
 * height with a back affordance), and a tab bar that steps aside for the
 * keyboard so editor input keeps the full height.
 */
class MainActivity : AppCompatActivity() {
    private lateinit var binding: ActivityMainBinding
    private var selectedTab = R.id.nav_home
    private var imeVisible = false
    private var navInset = 0

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        WindowCompat.setDecorFitsSystemWindows(window, false)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)
        binding.bottomBar.addOnLayoutChangeListener { _, _, _, _, _, _, _, _, _ -> updateBottomBar() }
        ViewCompat.setOnApplyWindowInsetsListener(binding.root) { _, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout())
            val ime = insets.getInsets(WindowInsetsCompat.Type.ime())
            imeVisible = insets.isVisible(WindowInsetsCompat.Type.ime()) && ime.bottom > 0
            // Status bar and cutout pad the chrome, not the page surface, so
            // content still reads as one sheet under the bar.
            binding.topBarContainer.setPadding(bars.left, bars.top, bars.right, 0)
            binding.fragmentContainer.setPadding(bars.left, 0, bars.right, 0)
            val density = resources.displayMetrics.density
            binding.bottomBar.setPadding(bars.left + (16 * density).toInt(), (8 * density).toInt(),
                bars.right + (16 * density).toInt(), (8 * density).toInt() + if (imeVisible) 0 else bars.bottom)
            binding.root.setPadding(0, 0, 0, if (imeVisible) ime.bottom else 0)
            navInset = bars.bottom
            updateBottomBar()
            WindowInsetsCompat.CONSUMED
        }
        ViewCompat.requestApplyInsets(binding.root)
        selectedTab = savedInstanceState?.getInt("selectedTab", R.id.nav_home) ?: R.id.nav_home
        binding.topAppBar.setNavigationOnClickListener { onBackPressedDispatcher.onBackPressed() }
        binding.bottomNav.setOnItemSelectedListener { id ->
            val editor = supportFragmentManager.primaryNavigationFragment as? com.pidal.sakamoto.ui.EditorFragment
            if (editor != null && editor.hasUnsavedChanges()) {
                editor.confirmLeaving { selectTab(id) }
                false
            } else { selectTab(id); true }
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
        binding.bottomNav.selectedItemId = id
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
        val topBackground = getColor(if (child) R.color.surface else R.color.surface_variant)
        binding.topBarContainer.setBackgroundColor(topBackground)
        binding.topAppBar.setBackgroundColor(topBackground)
        binding.bottomNav.selectedItemId = selectedTab
        binding.topAppBar.title = if (child) manager.getBackStackEntryAt(manager.backStackEntryCount - 1).name else when (selectedTab) {
            R.id.nav_config -> getString(R.string.tab_config)
            R.id.nav_data -> getString(R.string.tab_data)
            R.id.nav_settings -> getString(R.string.tab_settings)
            else -> getString(R.string.tab_home)
        }
        binding.topAppBar.navigationIcon = if (child) androidx.appcompat.content.res.AppCompatResources.getDrawable(this, androidx.appcompat.R.drawable.abc_ic_ab_back_material) else null
        binding.topAppBar.navigationContentDescription = getString(R.string.navigate_back)
        updateBottomBar()
    }

    /** Telegram grammar: tabs only on top-level pages, and never over the keyboard.
     * Without the tab bar, pages pad themselves above the gesture area. */
    private fun updateBottomBar() {
        val child = supportFragmentManager.backStackEntryCount > 0
        binding.bottomBar.visibility = if (imeVisible || child) View.GONE else View.VISIBLE
        binding.fragmentContainer.setPadding(
            binding.fragmentContainer.paddingLeft, 0, binding.fragmentContainer.paddingRight,
            when {
                imeVisible -> 0
                child -> navInset
                else -> binding.bottomBar.height
            }
        )
    }

    override fun onSaveInstanceState(outState: Bundle) {
        outState.putInt("selectedTab", selectedTab)
        super.onSaveInstanceState(outState)
    }
}
