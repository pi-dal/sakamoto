package com.pidal.sakamoto

import android.app.Activity
import android.app.Instrumentation
import android.content.Intent
import android.content.pm.PackageManager
import android.net.VpnService
import android.os.Bundle
import com.pidal.sakamoto.bg.TunnelBoxService
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.mobilecore.Mobilecore

/** Default: JNI only. Explicit -e vpn true uses an already-consented VPN/config. */
class NativeBindingSmokeTest : Instrumentation() {
    private var checkVpn = false
    private var checkUi = false
    private var probe = false
    private var checkSnapshot = false
    private var holdMillis = 0L
    private var controls = false
    private var widgetSizes = false
    private var homeLayout = false
    private var navigation = false
    private var maps = false
    private var locationSetup = false
    private var widgetToggle = false
    private var editors = false
    private var updates = false
    private var power = false
    private var recovery = false
    private var checkChain = false
    private var recoveryVpn = false
    private var profileCheck: String? = null

    override fun onCreate(arguments: Bundle?) {
        super.onCreate(arguments)
        profileCheck = arguments?.getString("profileIsolation")
        checkVpn = arguments?.getString("vpn") == "true"
        checkUi = arguments?.getString("ui") == "true"
        probe = arguments?.getString("probe") == "true"
        checkSnapshot = arguments?.getString("snapshot") == "true"
        controls = arguments?.getString("controls") == "true"
        widgetSizes = arguments?.getString("widgets") == "true"
        homeLayout = arguments?.getString("home") == "true"
        navigation = arguments?.getString("navigation") == "true"
        maps = arguments?.getString("maps") == "true"
        locationSetup = arguments?.getString("locationSetup") == "true"
        editors = arguments?.getString("editors") == "true"
        updates = arguments?.getString("updates") == "true"
        power = arguments?.getString("power") == "true"
        recovery = arguments?.getString("recovery") == "true"
        checkChain = arguments?.getString("chain") == "true"
        recoveryVpn = arguments?.getString("recoveryVpn") == "true"
        widgetToggle = arguments?.getString("toggle") == "true"
        if (widgetToggle) checkVpn = true
        holdMillis = arguments?.getString("holdMillis")?.toLongOrNull()?.coerceIn(0L, 30_000L) ?: 0L
        start()
    }

    private fun checkPages() {
        // Exercise the real Android row at narrow width, large type and RTL.
        // This lane creates no Activity and never reads private source data.
        runOnMainSync {
            val renders = mutableListOf<android.graphics.Bitmap>()
            for ((fontScale, rtl) in listOf(1f to false, 1.3f to false, 1.3f to true)) {
                val config = android.content.res.Configuration(targetContext.resources.configuration)
                config.fontScale = fontScale
                config.setLayoutDirection(java.util.Locale(if (rtl) "ar" else "en"))
                val context = android.view.ContextThemeWrapper(targetContext.createConfigurationContext(config), R.style.Theme_Sakamoto)
                val page = com.pidal.sakamoto.ui.GroupedPage(context)
                var rowClicks = 0
                var edits = 0
                var menus = 0
                val row = page.row(page.section("Nodes"), "Reality-Vision-Azure-JP-long-name", "VLESS · 128 ms", R.drawable.ic_node, navigates = false) { rowClicks++ }
                row.selectedNode(true)
                row.layoutDirection = if (rtl) android.view.View.LAYOUT_DIRECTION_RTL else android.view.View.LAYOUT_DIRECTION_LTR
                val edit = row.actionIcon(R.drawable.ic_edit, "Edit node") { edits++ }
                val menu = row.actionIcon(R.drawable.ic_more, "Node actions") { menus++ }
                val width = page.dp(360)
                row.measure(android.view.View.MeasureSpec.makeMeasureSpec(width, android.view.View.MeasureSpec.EXACTLY), android.view.View.MeasureSpec.makeMeasureSpec(0, android.view.View.MeasureSpec.UNSPECIFIED))
                row.layout(0, 0, width, row.measuredHeight)
                val editRect = android.graphics.Rect()
                val menuRect = android.graphics.Rect()
                edit.getDrawingRect(editRect); row.offsetDescendantRectToMyCoords(edit, editRect)
                menu.getDrawingRect(menuRect); row.offsetDescendantRectToMyCoords(menu, menuRect)
                val labelRect = android.graphics.Rect()
                row.label.getDrawingRect(labelRect); row.offsetDescendantRectToMyCoords(row.label, labelRect)
                check(editRect.width() >= page.dp(48) && editRect.height() >= page.dp(48))
                if (rtl) {
                    check(menuRect.right < editRect.left && editRect.right <= labelRect.left) { "RTL actions overlap content" }
                } else {
                    check(editRect.left >= labelRect.right && menuRect.left > editRect.right) { "Actions are not at trailing edge" }
                }
                edit.performClick(); menu.performClick()
                check(edits == 1 && menus == 1 && rowClicks == 0) { "Secondary actions selected the row" }
                row.performClick(); check(rowClicks == 1)
                val bitmap = android.graphics.Bitmap.createBitmap(width, row.measuredHeight, android.graphics.Bitmap.Config.ARGB_8888)
                val canvas = android.graphics.Canvas(bitmap)
                canvas.drawColor(context.getColor(R.color.surface))
                row.draw(canvas)
                renders.add(bitmap)
            }
            val result = android.graphics.Bitmap.createBitmap(renders.maxOf { it.width }, renders.sumOf { it.height }, android.graphics.Bitmap.Config.ARGB_8888)
            val canvas = android.graphics.Canvas(result)
            var offset = 0f
            renders.forEach { bitmap -> canvas.drawBitmap(bitmap, 0f, offset, null); offset += bitmap.height; bitmap.recycle() }
            java.io.File(targetContext.cacheDir, "row-action-verification.png").outputStream().use { result.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
            result.recycle()
        }
    }

    private fun checkNavigation() {
        val activity = startActivitySync(Intent(targetContext, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        waitForIdleSync()
        fun descendants(root: android.view.View): List<android.view.View> = listOf(root) +
            if (root is android.view.ViewGroup) (0 until root.childCount).flatMap { descendants(root.getChildAt(it)) } else emptyList()
        for ((id, name) in listOf(R.id.nav_config to "config", R.id.nav_data to "data", R.id.nav_settings to "settings")) {
            runOnMainSync { activity.findViewById<android.view.View>(id).performClick() }
            waitForIdleSync()
            runOnMainSync {
                val bar = activity.findViewById<com.pidal.sakamoto.ui.TelegramTabBar>(R.id.bottom_nav)
                check(bar.selectedItemId == id && bar.visibility == android.view.View.VISIBLE)
                val root = activity.window.decorView
                val text = descendants(activity.findViewById(R.id.fragment_container)).filterIsInstance<android.widget.TextView>().map { it.text.toString() }
                if (name == "settings") {
                    check(targetContext.getString(R.string.settings_tailscale_entry) in text)
                }
                if (name == "data") check(targetContext.getString(R.string.data_uplink) in text)
                val bitmap = android.graphics.Bitmap.createBitmap(root.width, root.height, android.graphics.Bitmap.Config.ARGB_8888)
                root.draw(android.graphics.Canvas(bitmap))
                java.io.File(targetContext.cacheDir, "telegram-$name.png").outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
                bitmap.recycle()
            }
        }
        runOnMainSync {
            activity.openChild(com.pidal.sakamoto.ui.AboutFragment(), targetContext.getString(R.string.settings_about_entry))
            activity.supportFragmentManager.executePendingTransactions()
            check(activity.findViewById<android.view.View>(R.id.bottom_bar).visibility == android.view.View.GONE)
            activity.supportFragmentManager.popBackStackImmediate()
            check(activity.findViewById<android.view.View>(R.id.bottom_bar).visibility == android.view.View.VISIBLE)
            for (night in listOf(false, true)) for (scale in listOf(1f, 1.3f)) for (rtl in listOf(false, true)) {
                val config = android.content.res.Configuration(targetContext.resources.configuration).apply {
                    fontScale = scale
                    uiMode = (uiMode and android.content.res.Configuration.UI_MODE_NIGHT_MASK.inv()) or
                        if (night) android.content.res.Configuration.UI_MODE_NIGHT_YES else android.content.res.Configuration.UI_MODE_NIGHT_NO
                    setLayoutDirection(java.util.Locale(if (rtl) "ar" else "en"))
                }
                val context = android.view.ContextThemeWrapper(targetContext.createConfigurationContext(config), R.style.Theme_Sakamoto)
                val bar = com.pidal.sakamoto.ui.TelegramTabBar(context)
                val density = context.resources.displayMetrics.density
                val width = (360 * density).toInt()
                bar.measure(android.view.View.MeasureSpec.makeMeasureSpec(width, android.view.View.MeasureSpec.EXACTLY), android.view.View.MeasureSpec.makeMeasureSpec(0, android.view.View.MeasureSpec.UNSPECIFIED))
                bar.layout(0, 0, bar.measuredWidth, bar.measuredHeight)
                for (index in 0 until bar.childCount) {
                    val tab = bar.getChildAt(index) as android.view.ViewGroup
                    check(tab.width >= 48 * density && tab.height >= 48 * density) { "Tab target is too small" }
                    val label = tab.getChildAt(1) as android.widget.TextView
                    check(label.height > 0 && label.layout != null && label.layout.lineCount > 0 &&
                        label.top >= 0 && label.bottom <= tab.height && label.layout.height <= label.height) {
                        "Tab label clipped at large type: h=${label.height} layout=${label.layout?.height} bottom=${label.bottom} tab=${tab.height}"
                    }
                }
                val bitmap = android.graphics.Bitmap.createBitmap(bar.width, bar.height, android.graphics.Bitmap.Config.ARGB_8888)
                bar.draw(android.graphics.Canvas(bitmap))
                java.io.File(targetContext.cacheDir, "telegram-tabs-$night-$scale-$rtl.png").outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
                bitmap.recycle()
            }
            activity.findViewById<android.view.View>(R.id.nav_home).performClick()
        }
    }

    private fun checkWidgetSizes() {
        runOnMainSync {
            val manager = android.appwidget.AppWidgetManager.getInstance(targetContext)
            val provider = com.pidal.sakamoto.widget.VpnWidgetProvider
            for (clazz in provider.providers) {
                val name = android.content.ComponentName(targetContext, clazz)
                check(manager.installedProviders.any { it.provider == name }) { "Widget size provider not registered" }
            }
            val renders = mutableListOf<android.graphics.Bitmap>()
            val sizes = mutableListOf(
                Triple(R.layout.widget_vpn_toggle, 56, 56),
                Triple(R.layout.widget_vpn_toggle, 80, 80),
                Triple(R.layout.widget_vpn_compact, 160, 64),
                Triple(R.layout.widget_vpn_compact, 200, 120),
                Triple(R.layout.widget_vpn_compact, 195, 107),
                Triple(R.layout.widget_vpn_compact, 195, 104),
                Triple(R.layout.widget_vpn, 250, 160),
            )
            val compactIds = manager.getAppWidgetIds(android.content.ComponentName(targetContext, com.pidal.sakamoto.widget.VpnCompactWidgetProvider::class.java))
            val actual = compactIds.firstOrNull()?.let { id ->
                val options = manager.getAppWidgetOptions(id)
                val width = options.getInt(android.appwidget.AppWidgetManager.OPTION_APPWIDGET_MIN_WIDTH)
                val height = options.getInt(android.appwidget.AppWidgetManager.OPTION_APPWIDGET_MIN_HEIGHT)
                if (width > 0 && height > 0) Triple(R.layout.widget_vpn_compact, width, height) else null
            }
            if (actual != null && actual !in sizes) sizes.add(actual)
            val sizeReport = actual?.let { "Actual launcher compact widget: ${it.second}×${it.third} dp" } ?: "No compact widget pinned"
            java.io.File(targetContext.cacheDir, "widget-launcher-size.txt").writeText(sizeReport)
            for ((layout, width, height) in sizes) {
                val configuration = android.content.res.Configuration(targetContext.resources.configuration).apply { fontScale = 1.3f }
                val context = android.view.ContextThemeWrapper(targetContext.createConfigurationContext(configuration), R.style.Theme_Sakamoto)
                val density = context.resources.displayMetrics.density
                fun dp(value: Int) = (value * density).toInt()
                val model = com.pidal.sakamoto.runtime.WidgetPresentation.Model(
                    com.pidal.sakamoto.runtime.WidgetPresentation.Health.VERIFIED,
                    "Reality-Vision-Azure-JP", 128,
                    com.pidal.sakamoto.runtime.WidgetPresentation.Latency.FRESH,
                    System.currentTimeMillis() / 1000, "Rule", "vless", "MainProxy")
                val remote = provider.render(context, layout, height, model, width)
                val root = remote.apply(context, android.widget.FrameLayout(context))
                root.measure(android.view.View.MeasureSpec.makeMeasureSpec(dp(width), android.view.View.MeasureSpec.EXACTLY), android.view.View.MeasureSpec.makeMeasureSpec(dp(height), android.view.View.MeasureSpec.EXACTLY))
                root.layout(0, 0, root.measuredWidth, root.measuredHeight)
                val button = root.findViewById<android.view.View>(R.id.widget_toggle)
                val rect = android.graphics.Rect()
                button.getDrawingRect(rect); (root as android.view.ViewGroup).offsetDescendantRectToMyCoords(button, rect)
                check(rect.width() >= dp(48) && rect.height() >= dp(48)) { "Widget toggle is too small" }
                check(rect.left >= 0 && rect.top >= 0 && rect.right <= root.width && rect.bottom <= root.height) { "Widget toggle is clipped" }
                if (layout == R.layout.widget_vpn_compact) {
                    if (root.findViewById<android.view.View>(R.id.widget_node_info).isShown) {
                        val info = root.findViewById<android.widget.TextView>(R.id.widget_node_info)
                        check(info.text.toString().contains("VLESS") && info.text.toString().contains("Rule"))
                        val infoRect = android.graphics.Rect(); info.getDrawingRect(infoRect); root.offsetDescendantRectToMyCoords(info, infoRect)
                        check(infoRect.bottom <= root.height) { "Node details clipped at actual launcher size" }
                    }
                }
                val bitmap = android.graphics.Bitmap.createBitmap(root.width, root.height, android.graphics.Bitmap.Config.ARGB_8888)
                root.draw(android.graphics.Canvas(bitmap)); renders.add(bitmap)
                val filename = "widget-${layout}-${width}x${height}.png"
                java.io.File(targetContext.cacheDir, filename).outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
            }
            val context = android.view.ContextThemeWrapper(targetContext, R.style.Theme_Sakamoto)
            for ((health, latency) in listOf(
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.RUNNING to com.pidal.sakamoto.runtime.WidgetPresentation.Latency.UNTESTED,
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.UNVERIFIED to com.pidal.sakamoto.runtime.WidgetPresentation.Latency.STALE,
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.OFF to com.pidal.sakamoto.runtime.WidgetPresentation.Latency.NONE,
            )) {
                val model = com.pidal.sakamoto.runtime.WidgetPresentation.Model(health, "Reality-Vision-Azure-JP", 128, latency, System.currentTimeMillis() / 1000 - 200, "Rule")
                val root = provider.render(context, R.layout.widget_vpn, 160, model).apply(context, android.widget.FrameLayout(context))
                val density = context.resources.displayMetrics.density
                root.measure(android.view.View.MeasureSpec.makeMeasureSpec((250 * density).toInt(), android.view.View.MeasureSpec.EXACTLY), android.view.View.MeasureSpec.makeMeasureSpec((160 * density).toInt(), android.view.View.MeasureSpec.EXACTLY))
                root.layout(0, 0, root.measuredWidth, root.measuredHeight)
                val text = root.findViewById<android.widget.TextView>(R.id.widget_latency).text.toString()
                check(if (latency == com.pidal.sakamoto.runtime.WidgetPresentation.Latency.STALE) text.endsWith("*") else !text.contains("128")) { "Stale or untested latency was presented as fresh" }
                val bitmap = android.graphics.Bitmap.createBitmap(root.width, root.height, android.graphics.Bitmap.Config.ARGB_8888)
                root.draw(android.graphics.Canvas(bitmap)); renders.add(bitmap)
            }
            val image = android.graphics.Bitmap.createBitmap(renders.maxOf { it.width }, renders.sumOf { it.height } + renders.size * 24, android.graphics.Bitmap.Config.ARGB_8888)
            val canvas = android.graphics.Canvas(image); canvas.drawColor(android.graphics.Color.DKGRAY)
            var y = 12f
            renders.forEach { bitmap -> canvas.drawBitmap(bitmap, 0f, y, null); y += bitmap.height + 24; bitmap.recycle() }
            java.io.File(targetContext.cacheDir, "widget-size-verification.png").outputStream().use { image.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
            image.recycle()
        }
    }

    private fun checkHomeLayout() {
        runOnMainSync {
            val renders = mutableListOf<android.graphics.Bitmap>()
            for ((scale, title, subtitle) in listOf(
                Triple(1f, "Disconnected", "Turn on to connect"),
                Triple(1.3f, "VPN running", "Network not verified yet"),
                Triple(1.3f, "Connection unverified", "Network check timed out"),
            )) {
                val config = android.content.res.Configuration(targetContext.resources.configuration).apply { fontScale = scale }
                val context = android.view.ContextThemeWrapper(targetContext.createConfigurationContext(config), R.style.Theme_Sakamoto)
                val binding = com.pidal.sakamoto.databinding.FragmentHomeBinding.inflate(android.view.LayoutInflater.from(context))
                binding.phaseValue.text = title
                binding.phaseDescription.text = subtitle
                val density = context.resources.displayMetrics.density
                fun dp(value: Int) = (value * density).toInt()
                binding.root.measure(android.view.View.MeasureSpec.makeMeasureSpec(dp(360), android.view.View.MeasureSpec.EXACTLY), android.view.View.MeasureSpec.makeMeasureSpec(dp(780), android.view.View.MeasureSpec.EXACTLY))
                binding.root.layout(0, 0, binding.root.measuredWidth, binding.root.measuredHeight)
                val statusRect = android.graphics.Rect()
                val switchRect = android.graphics.Rect()
                binding.connectionStatusEntry.getDrawingRect(statusRect)
                binding.root.offsetDescendantRectToMyCoords(binding.connectionStatusEntry, statusRect)
                binding.connectionSwitch.getDrawingRect(switchRect)
                binding.root.offsetDescendantRectToMyCoords(binding.connectionSwitch, switchRect)
                check(statusRect.height() >= dp(48)) { "Status entry is too small" }
                check(statusRect.right <= switchRect.left) { "Status overlaps connection control" }
                binding.connectionSwitch.isChecked = title != "Disconnected"
                val container = binding.root.getChildAt(0) as android.view.ViewGroup
                val header = container.getChildAt(0)
                val bitmap = android.graphics.Bitmap.createBitmap(header.width, header.height, android.graphics.Bitmap.Config.ARGB_8888)
                header.draw(android.graphics.Canvas(bitmap))
                renders.add(bitmap)
            }
            val image = android.graphics.Bitmap.createBitmap(renders.maxOf { it.width }, renders.sumOf { it.height } + renders.size * 24, android.graphics.Bitmap.Config.ARGB_8888)
            val canvas = android.graphics.Canvas(image); canvas.drawColor(android.graphics.Color.DKGRAY)
            var top = 12f
            renders.forEach { bitmap -> canvas.drawBitmap(bitmap, 0f, top, null); top += bitmap.height + 24; bitmap.recycle() }
            java.io.File(targetContext.cacheDir, "home-connection-layout.png").outputStream().use { image.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
            image.recycle()
        }
    }

    override fun onStart() {
        val results = Bundle()
        try {
            // Instrumentation starts concurrently with Application.onCreate.
            // Wait for main-thread native setup before touching gomobile classes;
            // otherwise Libbox/SetupOptions static initializers can deadlock.
            runOnMainSync { }
            check(Mobilecore.sessionPhase("Running", "Reachable", false) == "Reachable")
            check(Mobilecore.nextRoutingMode("Rule") == "Global")
            check(Libbox.version().isNotBlank())
            val experiment = io.nekohasekai.mobileexperiment.Mobileexperiment.newExperimentTracker(1)
            experiment.connection("test", "example.com", "93.184.215.14:443", "tcp", "direct", "", false, 0, 1_000_000)
            check(experiment.observeLog("connection: open connection to example.com:443 using outbound/direct[direct]: dial tcp 93.184.215.14:443: i/o timeout", 1_000_000, true, true, false) == "example.com")
            android.widget.RemoteViews(targetContext.packageName, R.layout.widget_vpn).apply(targetContext, android.widget.FrameLayout(targetContext))
            Mobilecore.validateConfigJSON("{\"log\":{},\"dns\":{},\"inbounds\":[{\"type\":\"tun\"}],\"outbounds\":[{\"type\":\"direct\",\"tag\":\"direct\"}],\"route\":{\"final\":\"direct\"}}")
            check(targetContext.checkSelfPermission("android.permission.ACCESS_NETWORK_STATE") == PackageManager.PERMISSION_GRANTED)
            if (checkUi) checkPages()
            if (widgetSizes) checkWidgetSizes()
            if (homeLayout) checkHomeLayout()
            val vpnIntent = Intent(VpnService.SERVICE_INTERFACE).setPackage(targetContext.packageName)
            @Suppress("DEPRECATION")
            val vpnServices = targetContext.packageManager.queryIntentServices(vpnIntent, 0)
            check(vpnServices.any { it.serviceInfo.permission == "android.permission.BIND_VPN_SERVICE" }) { "VPN service registration is missing" }
            if (checkSnapshot) {
                val staged = com.pidal.sakamoto.runtime.ConfigRepository.load(targetContext)
                val metadata = org.json.JSONObject(staged.hostMetadata)
                check(metadata.has("chain_enabled") && metadata.has("fallbacks"))
                val saved = com.pidal.sakamoto.runtime.RoutingSnapshot.parse(staged.generatedContent)
                check(saved.rules.isNotEmpty())
                check(saved.exits.any { exit -> saved.rules.any { it.outbound == exit.tag } })
                check(com.pidal.sakamoto.runtime.ConfigRepository.sourceFiles(targetContext).any { it.name == "policy.json" })
                check(com.pidal.sakamoto.runtime.ConfigRepository.sourceFiles(targetContext).any { it.name == "chain.json" })
            }
            profileCheck?.let {
                when {
                    it == "historical-installed" -> {
                        val ctx = targetContext
                        val before = com.pidal.sakamoto.runtime.ConfigRepository.manifestReceipt(ctx)
                        val activity = startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
                        try {
                            waitForIdleSync()
                            runOnMainSync { activity.findViewById<android.view.View>(R.id.repair_vpn).performClick() }
                            val deadline = System.nanoTime() + 5_000_000_000L
                            var detail = ""
                            while (System.nanoTime() < deadline && detail.isEmpty()) {
                                runOnMainSync {
                                    val row = activity.findViewById<android.widget.TextView>(R.id.repair_result)
                                    val state = com.pidal.sakamoto.runtime.MobilecoreRuntime.state.value
                                    if (row.isShown && !state.repairing) detail = row.text.toString()
                                }
                                if (detail.isEmpty()) Thread.sleep(40)
                            }
                            check(detail == "Re-export this older package with the current desktop app to verify the domain routes.") { "Home Repair displayed: $detail" }
                            check(!com.pidal.sakamoto.bg.TunnelBoxService.hasOwner() && com.pidal.sakamoto.bg.TunnelBoxService.verifiedInstance() == 0L)
                            check(before == com.pidal.sakamoto.runtime.ConfigRepository.manifestReceipt(ctx))
                        } finally { runOnMainSync { activity.finish() } }
                        com.pidal.sakamoto.bg.TunnelBoxService.start(ctx)
                        val end = System.nanoTime() + 10_000_000_000L
                        while (com.pidal.sakamoto.runtime.MobilecoreRuntime.state.value.notice?.startsWith("Re-export this older package") != true && System.nanoTime() < end) Thread.sleep(40)
                        check(com.pidal.sakamoto.runtime.MobilecoreRuntime.state.value.notice?.startsWith("Re-export this older package") == true)
                        check(com.pidal.sakamoto.bg.TunnelBoxService.verifiedInstance() == 0L)
                        check(before == com.pidal.sakamoto.runtime.ConfigRepository.manifestReceipt(ctx))
                    }
                    it == "historical-learned" || it == "historical-scalar" -> {
                        val filename = "$it.zip"
                        val ctx = targetContext
                        val before = com.pidal.sakamoto.runtime.ConfigRepository.manifestReceipt(ctx)
                        val error = runCatching {
                            java.io.File(ctx.cacheDir, filename).inputStream().use { input ->
                                com.pidal.sakamoto.runtime.TunnelPackage.import(ctx, input)
                            }
                        }.exceptionOrNull()
                        check(error?.message?.startsWith("Re-export this older package") == true) { "Expected legacy route warning" }
                        check(before == com.pidal.sakamoto.runtime.ConfigRepository.manifestReceipt(ctx))
                    }
                    it.startsWith("s3-") -> S3ProfileScenario.run(this, it)
                    it.startsWith("usb-") -> UsbEditScenario.run(this, it)
                    it.startsWith("vpn-") -> VpnProfileScenario.run(this, it)
                    else -> ProfileIsolationScenario.run(this, it)
                }
            }
            if (checkChain) ProxyChainScenario.run(this)
            if (power) PowerObservationScenario.run(this)
            if (recovery) RecoveryObservationScenario.run(this)
            if (recoveryVpn) RecoveryObservationScenario.runVpn(this)
            if (navigation) checkNavigation()
            if (maps) LocationMapSmokeChecks(this).run()
            if (editors) NodeGroupSmokeChecks(this).run()
            if (updates) AppUpdateSmokeChecks(this).run()
            if (locationSetup) LocationSetupSmokeChecks(this).run()
            if (checkVpn) {
                check(VpnService.prepare(targetContext) == null) { "VPN consent is required" }
                if (widgetToggle) com.pidal.sakamoto.runtime.SystemStatusSurface.toggle(targetContext).send()
                else TunnelBoxService.start(targetContext)
                val deadline = System.currentTimeMillis() + 15_000
                while (MobilecoreRuntime.state.value.serviceState != "Running" && System.currentTimeMillis() < deadline) {
                    Thread.sleep(100)
                    if (!MobilecoreRuntime.state.value.notice.isNullOrEmpty()) break
                }
                val state = MobilecoreRuntime.state.value
                check(state.serviceState == "Running") { "VPN did not reach Running: ${state.notice}" }
                Thread.sleep(1000)
                check(MobilecoreRuntime.state.value.routingMode.isNotEmpty()) { "Routing mode stream is missing" }
                if (widgetToggle) {
                    val action = com.pidal.sakamoto.runtime.SystemStatusSurface.toggle(targetContext)
                    // Click-time state must decide direction, even using the
                    // exact same PendingIntent cached while disconnected.
                    action.send()
                    val stopDeadline = System.currentTimeMillis() + 5000
                    while (MobilecoreRuntime.state.value.serviceState != "Stopped" && System.currentTimeMillis() < stopDeadline) Thread.sleep(100)
                    check(MobilecoreRuntime.state.value.serviceState == "Stopped") { "Widget toggle did not disconnect" }
                    val disconnectDeadline = System.currentTimeMillis() + 5000
                    while (com.pidal.sakamoto.runtime.VpnDiagnostics.snapshot(targetContext).vpn && System.currentTimeMillis() < disconnectDeadline) Thread.sleep(100)
                    check(!com.pidal.sakamoto.runtime.VpnDiagnostics.snapshot(targetContext).vpn) { "Widget left a system VPN behind" }
                    Thread.sleep(500)
                    action.send()
                    val startDeadline = System.currentTimeMillis() + 15000
                    while (MobilecoreRuntime.state.value.serviceState != "Running" && System.currentTimeMillis() < startDeadline) Thread.sleep(100)
                    check(MobilecoreRuntime.state.value.serviceState == "Running") { "Widget toggle did not reconnect: ${MobilecoreRuntime.state.value.serviceState}/${MobilecoreRuntime.state.value.notice}" }
                    check(MobilecoreRuntime.state.value.notice.isNullOrEmpty()) { "Widget toggle failed" }
                }
                if (controls) {
                    val command = com.pidal.sakamoto.command.CommandClientRuntime
                    val oldMode = MobilecoreRuntime.state.value.routingMode
                    command.setClashMode(oldMode)
                    check(command.reloadService()) { "Reload RPC failed" }
                    val reloadDeadline = System.currentTimeMillis() + 10_000
                    while ((MobilecoreRuntime.state.value.configApplyPending || MobilecoreRuntime.state.value.serviceState != "Running") && System.currentTimeMillis() < reloadDeadline) Thread.sleep(100)
                    check(MobilecoreRuntime.state.value.serviceState == "Running") { "Reload stopped service" }
                    check(MobilecoreRuntime.state.value.configState == "Clean") { "Reload not acknowledged" }
                    val notification = com.pidal.sakamoto.runtime.SystemStatusSurface.notification(targetContext)
                    check(notification.actions.size == 3)
                    notification.actions[1].actionIntent.send()
                    val probeDeadline = System.currentTimeMillis() + 20_000
                    while (MobilecoreRuntime.state.value.probeAt == 0L && System.currentTimeMillis() < probeDeadline) Thread.sleep(100)
                    check(MobilecoreRuntime.state.value.probeState == "Reachable") { "Notification probe did not pass: ${MobilecoreRuntime.state.value.probeState}/${MobilecoreRuntime.state.value.probeDetail}/${MobilecoreRuntime.state.value.notice}" }
                    val display = com.pidal.sakamoto.runtime.SystemStatusSurface.display(targetContext)
                    check(display.running && display.title == targetContext.getString(R.string.phase_reachable))
                    val manager = android.appwidget.AppWidgetManager.getInstance(targetContext)
                    val provider = android.content.ComponentName(targetContext, com.pidal.sakamoto.widget.VpnWidgetProvider::class.java)
                    check(manager.installedProviders.any { it.provider == provider }) { "Widget provider missing" }
                    val widgetProjection = com.pidal.sakamoto.runtime.WidgetPresentation.resolve(MobilecoreRuntime.state.value, true, true, command.groups.value, System.currentTimeMillis() / 1000)
                    check(widgetProjection.health == com.pidal.sakamoto.runtime.WidgetPresentation.Health.VERIFIED)
                    for (layout in listOf(R.layout.widget_vpn_toggle, R.layout.widget_vpn_compact, R.layout.widget_vpn)) {
                        com.pidal.sakamoto.widget.VpnWidgetProvider.views(targetContext, layout, 160)
                            .apply(targetContext, android.widget.FrameLayout(targetContext))
                    }
                    com.pidal.sakamoto.widget.VpnWidgetProvider.updateAll(targetContext)
                    notification.actions[0].actionIntent.send()
                    val stopDeadline = System.currentTimeMillis() + 5000
                    while (MobilecoreRuntime.state.value.serviceState != "Stopped" && System.currentTimeMillis() < stopDeadline) Thread.sleep(100)
                    check(MobilecoreRuntime.state.value.serviceState == "Stopped") { "Notification stop failed" }
                    check(!com.pidal.sakamoto.runtime.SystemStatusSurface.display(targetContext).running)
                }
                if (probe) {
                    kotlinx.coroutines.runBlocking { com.pidal.sakamoto.runtime.VpnDiagnostics.probe(targetContext) }
                    val observed = MobilecoreRuntime.state.value
                    check(observed.probeState == "Reachable") { "Application Check failed: ${observed.probeDetail}" }
                    check(observed.probeAt > 0 && observed.phase == "Reachable") { "Application Check result was not published" }
                }
            }
            if (checkVpn && holdMillis > 0) {
                sendStatus(0, Bundle().apply { putString("stream", "VPN running: system inspection window\n") })
                Thread.sleep(holdMillis)
            }
            results.putString("stream", "PASS: JNI + network permission + VPN service registration${if (editors) " + node edit/save/delete/undo + group member reorder/save/reopen" else ""}${if (updates) " + updater published APK/signature/cache/tamper protection/native states" else ""}${if (checkUi) " + trailing row actions/48dp/large font/RTL/click isolation" else ""}${if (navigation) " + Telegram tabs/Config/Data/Settings/back/font1.3/RTL/light-dark" else ""}${if (locationSetup) " + location setup settings round-trip/permission-only grant/map/route jump/reopen" else ""}${if (maps) " + native map style/GL capture/route append/undo/commit/cancel without location permission" else ""}${if (checkVpn) " + real VPN startup + mode stream" else ""}${if (probe) " + application Check DNS/TLS/HTTPS204 + Reachable state" else ""}${if (checkSnapshot) " + host policy/exit snapshot" else ""}${if (controls) " + native reload + notification check/stop + widget provider" else ""}${if (widgetSizes) " + three widget providers/resizing/48dp at font1.3" else ""}${if (homeLayout) " + Home connection controls/font1.3/touch targets" else ""}${if (widgetToggle) " + widget direct connect/disconnect/reconnect" else ""}\n")
            finish(Activity.RESULT_OK, results)
        } catch (error: Throwable) {
            if (power) java.io.File(targetContext.cacheDir, "power-learning-failure.txt").writeText(error.stackTraceToString())
            if (recovery || recoveryVpn) java.io.File(targetContext.cacheDir, "mobile-recovery-failure.txt").writeText(error.stackTraceToString())
            val safe = error.message.orEmpty().replace(Regex("https?://\\S+"), "[URL]").take(200)
            results.putString("stream", "FAIL: smoke test: ${error.javaClass.simpleName}: $safe\n")
            finish(Activity.RESULT_CANCELED, results)
        } finally {
            if (checkVpn) TunnelBoxService.stop(targetContext)
        }
    }
}
