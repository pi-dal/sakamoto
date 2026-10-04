package com.pidal.sakamoto

import android.os.Bundle
import androidx.appcompat.app.AppCompatActivity
import com.google.android.material.color.DynamicColors
import com.pidal.sakamoto.databinding.ActivityMainBinding
import com.pidal.sakamoto.ui.AboutFragment
import com.pidal.sakamoto.ui.ConfigFragment
import com.pidal.sakamoto.ui.DataFragment
import com.pidal.sakamoto.ui.HomeFragment
import com.pidal.sakamoto.ui.SettingsFragment

/**
 * Single-activity shell: a bottom bar (Home / Config / Data / Settings — the
 * TUI's page set) over one fragment container.
 */
class MainActivity : AppCompatActivity() {

    private lateinit var binding: ActivityMainBinding

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        DynamicColors.applyToActivityIfAvailable(this)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)

        binding.bottomNav.setOnItemSelectedListener { item ->
            binding.topAppBar.title = when (item.itemId) {
                R.id.nav_config -> getString(R.string.tab_config)
                R.id.nav_data -> getString(R.string.tab_data)
                R.id.nav_settings -> getString(R.string.tab_settings)
                R.id.nav_about -> getString(R.string.tab_about)
                else -> getString(R.string.tab_home)
            }
            val fragment = when (item.itemId) {
                R.id.nav_config -> ConfigFragment()
                R.id.nav_data -> DataFragment()
                R.id.nav_settings -> SettingsFragment()
                R.id.nav_about -> AboutFragment()
                else -> HomeFragment()
            }
            supportFragmentManager.beginTransaction()
                .replace(R.id.fragment_container, fragment)
                .commit()
            true
        }

        if (savedInstanceState == null) {
            binding.bottomNav.selectedItemId = R.id.nav_home
        }
    }
}
