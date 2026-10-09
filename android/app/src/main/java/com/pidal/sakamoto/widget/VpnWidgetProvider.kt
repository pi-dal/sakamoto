package com.pidal.sakamoto.widget

import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.os.Bundle
import android.view.View
import android.widget.RemoteViews
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import com.pidal.sakamoto.runtime.SystemStatusSurface

/** Separate launcher entries share live status/actions; existing full widgets keep their identity. */
open class VpnWidgetProvider : AppWidgetProvider() {
    override fun onUpdate(context: Context, manager: AppWidgetManager, ids: IntArray) = update(context, manager, ids)
    override fun onAppWidgetOptionsChanged(context: Context, manager: AppWidgetManager, id: Int, options: Bundle) =
        update(context, manager, intArrayOf(id))

    companion object {
        val providers = listOf(VpnToggleWidgetProvider::class.java, VpnCompactWidgetProvider::class.java, VpnWidgetProvider::class.java)

        fun updateAll(context: Context) {
            val manager = AppWidgetManager.getInstance(context)
            providers.forEach { provider -> update(context, manager, manager.getAppWidgetIds(ComponentName(context, provider))) }
        }

        fun layoutFor(provider: String, minWidth: Int, minHeight: Int, fontScale: Float = 1f): Int = when {
            provider.endsWith("VpnToggleWidgetProvider") -> R.layout.widget_vpn_toggle
            provider.endsWith("VpnCompactWidgetProvider") -> if (minWidth in 1..119) R.layout.widget_vpn_toggle else R.layout.widget_vpn_compact
            minWidth in 1..119 -> R.layout.widget_vpn_toggle
            minHeight > 0 && minHeight < (132 * fontScale.coerceAtLeast(1f)).toInt() || minWidth in 1..219 -> R.layout.widget_vpn_compact
            else -> R.layout.widget_vpn
        }

        fun views(context: Context, layout: Int, minHeight: Int = 0, minWidth: Int = 160): RemoteViews {
            val runtime = MobilecoreRuntime.state.value
            val system = com.pidal.sakamoto.runtime.VpnDiagnostics.snapshot(context)
            val model = com.pidal.sakamoto.runtime.WidgetPresentation.resolve(runtime, system.vpn, system.physical.isNotEmpty(),
                com.pidal.sakamoto.command.CommandClientRuntime.groups.value, System.currentTimeMillis() / 1000)
            return render(context, layout, minHeight, model, minWidth)
        }

        /** A value projection also permits synthetic render tests without changing VPN state. */
        fun render(context: Context, layout: Int, minHeight: Int, model: com.pidal.sakamoto.runtime.WidgetPresentation.Model, minWidth: Int = 160): RemoteViews {
            val health = model.health
            val title = context.getString(when (health) {
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.OFF -> R.string.widget_off
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.BUSY -> R.string.widget_connecting
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.UNCONFIRMED -> R.string.widget_confirm
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.NO_NETWORK -> R.string.widget_no_network
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.CHECKING -> R.string.widget_checking
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.VERIFIED -> R.string.widget_verified
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.UNVERIFIED -> R.string.widget_unverified
                else -> R.string.widget_running
            })
            val running = health in setOf(com.pidal.sakamoto.runtime.WidgetPresentation.Health.RUNNING, com.pidal.sakamoto.runtime.WidgetPresentation.Health.VERIFIED,
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.UNVERIFIED, com.pidal.sakamoto.runtime.WidgetPresentation.Health.CHECKING, com.pidal.sakamoto.runtime.WidgetPresentation.Health.NO_NETWORK)
            val latency = context.getString(when (model.latency) {
                com.pidal.sakamoto.runtime.WidgetPresentation.Latency.NONE -> R.string.widget_latency_empty
                com.pidal.sakamoto.runtime.WidgetPresentation.Latency.UNTESTED -> R.string.widget_latency_untested
                com.pidal.sakamoto.runtime.WidgetPresentation.Latency.TESTING -> R.string.widget_checking
                com.pidal.sakamoto.runtime.WidgetPresentation.Latency.FAILED -> R.string.widget_latency_failed
                else -> R.string.widget_latency_value
            }, model.delay)
            val color = context.getColor(when (health) {
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.VERIFIED -> R.color.widget_success
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.NO_NETWORK, com.pidal.sakamoto.runtime.WidgetPresentation.Health.UNVERIFIED -> R.color.widget_warning
                com.pidal.sakamoto.runtime.WidgetPresentation.Health.OFF, com.pidal.sakamoto.runtime.WidgetPresentation.Health.UNCONFIRMED -> R.color.on_surface_variant
                else -> R.color.primary
            })
            val view = RemoteViews(context.packageName, layout)
            if (layout == R.layout.widget_vpn) {
                view.setInt(R.id.widget_mode, "setMaxWidth", ((minWidth * 0.25f) * context.resources.displayMetrics.density).toInt())
            }
            val openStatus = SystemStatusSurface.open(context, SystemStatusSurface.ACTION_STATUS)
            val toggle = SystemStatusSurface.toggle(context)
            view.setOnClickPendingIntent(R.id.widget_root, if (layout == R.layout.widget_vpn) openStatus else toggle)
            view.setInt(R.id.widget_state_dot, "setColorFilter", color)
            view.setInt(R.id.widget_toggle, "setColorFilter", if (running) color else context.getColor(R.color.on_surface_variant))
            view.setOnClickPendingIntent(R.id.widget_toggle, toggle)
            view.setBoolean(R.id.widget_toggle, "setEnabled", health != com.pidal.sakamoto.runtime.WidgetPresentation.Health.BUSY)
            view.setContentDescription(R.id.widget_toggle, title + ". " + context.getString(if (running) R.string.disconnect else R.string.connect))
            val node = if (running) model.node else ""
            view.setContentDescription(R.id.widget_root, "$title. $node. $latency. " + context.getString(R.string.widget_node_latency))
            if (layout == R.layout.widget_vpn_compact) {
                // Actual launcher SizeF controls optional detail; do not
                // combine portrait width with landscape's minimum height.
                // Visibility depends on size only. Switching the VPN must
                // never collapse the node slots and move the header.
                val fontScale = context.resources.configuration.fontScale.coerceAtLeast(1f)
                val detailHeight = (48 + 36 * fontScale).toInt()
                val showNode = minHeight >= detailHeight && minWidth >= 176
                view.setViewVisibility(R.id.widget_detail, if (showNode) View.VISIBLE else View.GONE)
                view.setViewVisibility(R.id.widget_node_info, if (showNode) View.VISIBLE else View.GONE)
                val density = context.resources.displayMetrics.density
                val tall = minHeight >= 88 && minWidth >= 176
                val controlSize = 48
                val textWidth = ((minWidth.coerceAtLeast(120) - controlSize - 24) * density).toInt()
                view.setTextViewTextSize(R.id.widget_title, android.util.TypedValue.COMPLEX_UNIT_SP, if (tall) 12f else 11f)
                view.setTextViewTextSize(R.id.widget_latency, android.util.TypedValue.COMPLEX_UNIT_SP, if (tall) 16f else 12f)
                if (android.os.Build.VERSION.SDK_INT >= 31) {
                    view.setViewLayoutWidth(R.id.widget_toggle, controlSize.toFloat(), android.util.TypedValue.COMPLEX_UNIT_DIP)
                    view.setViewLayoutHeight(R.id.widget_toggle, controlSize.toFloat(), android.util.TypedValue.COMPLEX_UNIT_DIP)
                    val columnWidth = (minWidth.coerceAtLeast(120) - 72).coerceIn(48, 112)
                    view.setViewLayoutWidth(R.id.widget_compact_measurement, columnWidth.toFloat(), android.util.TypedValue.COMPLEX_UNIT_DIP)
                    val headerHeight = 48f
                    view.setViewLayoutHeight(R.id.widget_compact_header, headerHeight, android.util.TypedValue.COMPLEX_UNIT_DIP)
                    view.setViewLayoutHeight(R.id.widget_compact_measurement, headerHeight, android.util.TypedValue.COMPLEX_UNIT_DIP)
                }
                view.setInt(R.id.widget_title, "setMaxWidth", (textWidth - 12 * density).toInt())
                view.setInt(R.id.widget_latency, "setMaxWidth", textWidth)
                view.setInt(R.id.widget_detail, "setMaxWidth", ((minWidth.coerceAtLeast(120) - 24) * density).toInt())
                view.setInt(R.id.widget_node_info, "setMaxWidth", ((minWidth.coerceAtLeast(120) - 24) * density).toInt())
            }
            if (layout == R.layout.widget_vpn_toggle) {
                view.setTextViewText(R.id.widget_title, if (model.latency == com.pidal.sakamoto.runtime.WidgetPresentation.Latency.FRESH) latency else title)
                view.setViewVisibility(R.id.widget_title, if (minHeight in 1..71) View.GONE else View.VISIBLE)
                view.setOnClickPendingIntent(R.id.widget_title, toggle)
            } else {
                view.setTextViewText(R.id.widget_title, title)
                view.setTextViewText(R.id.widget_latency, latency + if (model.latency == com.pidal.sakamoto.runtime.WidgetPresentation.Latency.STALE) "*" else "")
                view.setTextColor(R.id.widget_latency, context.getColor(if (model.latency == com.pidal.sakamoto.runtime.WidgetPresentation.Latency.FRESH) R.color.primary else R.color.on_surface_variant))
                view.setTextViewText(R.id.widget_detail, node.ifEmpty { context.getString(if (running) R.string.no_selected_node else R.string.widget_tap_connect) })
                val nodeInfo = if (running) listOf(model.protocol.uppercase(), model.mode, model.group).filter { it.isNotEmpty() }.joinToString(" · ").ifEmpty { context.getString(R.string.widget_waiting_node) }
                    else context.getString(R.string.widget_disconnected_info)
                view.setTextViewText(R.id.widget_node_info, nodeInfo)
                if (layout == R.layout.widget_vpn) view.setViewVisibility(R.id.widget_node_info, if (nodeInfo.isEmpty()) View.GONE else View.VISIBLE)
                if (layout == R.layout.widget_vpn) {
                    view.setTextViewText(R.id.widget_mode, if (running) model.mode.ifEmpty { "—" } else "")
                    val hasMeasurement = model.latency in setOf(com.pidal.sakamoto.runtime.WidgetPresentation.Latency.FRESH, com.pidal.sakamoto.runtime.WidgetPresentation.Latency.STALE, com.pidal.sakamoto.runtime.WidgetPresentation.Latency.FAILED)
                    val timestamp = if (hasMeasurement && model.testedAt > 0) android.text.format.DateFormat.format("HH:mm", java.util.Date(model.testedAt * 1000)).toString() else ""
                    view.setTextViewText(R.id.widget_latency_label, context.getString(if (model.latency == com.pidal.sakamoto.runtime.WidgetPresentation.Latency.STALE) R.string.widget_latency_stale else R.string.widget_latency_short_label) + if (timestamp.isNotEmpty()) " · $timestamp" else "")
                    val experiment = com.pidal.sakamoto.runtime.ExperimentRuntime.settings(context)
                    view.setTextViewText(R.id.widget_experiment, context.getString(R.string.widget_experiment_short, experiment.mode, com.pidal.sakamoto.runtime.ExperimentRuntime.learned(context).size))
                    view.setOnClickPendingIntent(R.id.widget_check, if (running) SystemStatusSurface.pending(context, SystemStatusSurface.ACTION_CHECK) else openStatus)
                    view.setContentDescription(R.id.widget_check, context.getString(R.string.vpn_check_now))
                }
            }
            return view
        }

        fun update(context: Context, manager: AppWidgetManager, ids: IntArray) {
            ids.forEach { id ->
                val info = manager.getAppWidgetInfo(id) ?: return@forEach
                val options = manager.getAppWidgetOptions(id)
                val minWidth = options.getInt(AppWidgetManager.OPTION_APPWIDGET_MIN_WIDTH).takeIf { it > 0 } ?: 160
                val minHeight = options.getInt(AppWidgetManager.OPTION_APPWIDGET_MIN_HEIGHT).takeIf { it > 0 } ?: 56
                val maxWidth = options.getInt(AppWidgetManager.OPTION_APPWIDGET_MAX_WIDTH).takeIf { it > 0 } ?: minWidth
                val maxHeight = options.getInt(AppWidgetManager.OPTION_APPWIDGET_MAX_HEIGHT).takeIf { it > 0 } ?: minHeight
                @Suppress("DEPRECATION")
                val sizes = if (android.os.Build.VERSION.SDK_INT >= 31) options.getParcelableArrayList<android.util.SizeF>(AppWidgetManager.OPTION_APPWIDGET_SIZES).orEmpty() else emptyList()
                android.util.Log.d("SakamotoWidgetSize", "id=$id min=${minWidth}x$minHeight max=${maxWidth}x$maxHeight sizes=$sizes")
                if (android.os.Build.VERSION.SDK_INT >= 31 && sizes.isNotEmpty()) {
                    val variants = sizes.distinct().take(16).associateWith { size ->
                        val layout = layoutFor(info.provider.className, size.width.toInt(), size.height.toInt(), context.resources.configuration.fontScale)
                        views(context, layout, size.height.toInt(), size.width.toInt())
                    }
                    manager.updateAppWidget(id, RemoteViews(variants))
                } else {
                    // Legacy min/max combine different orientations; portrait
                    // is minWidth×maxHeight, not minWidth×minHeight.
                    val portrait = views(context, layoutFor(info.provider.className, minWidth, maxHeight, context.resources.configuration.fontScale), maxHeight, minWidth)
                    val landscape = views(context, layoutFor(info.provider.className, maxWidth, minHeight, context.resources.configuration.fontScale), minHeight, maxWidth)
                    manager.updateAppWidget(id, RemoteViews(landscape, portrait))
                }
            }
        }
    }
}

class VpnToggleWidgetProvider : VpnWidgetProvider()
class VpnCompactWidgetProvider : VpnWidgetProvider()
