package com.pidal.sakamoto.ui

import android.content.Context
import android.content.res.ColorStateList
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.RippleDrawable
import android.util.AttributeSet
import android.view.Gravity
import android.view.MenuInflater
import android.view.View
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import androidx.core.graphics.ColorUtils
import androidx.core.view.ViewCompat
import com.pidal.sakamoto.R

/** Native Views implementation of the current Telegram Android tab geometry.
 * A bounded floating surface, with selection behind the entire icon/label pair.
 * Uses an opaque surface fallback rather than pretending to provide backdrop blur.
 */
class TelegramTabBar @JvmOverloads constructor(context: Context, attrs: AttributeSet? = null) : LinearLayout(context, attrs) {
    private val items = linkedMapOf<Int, LinearLayout>()
    private var onSelect: ((Int) -> Boolean)? = null
    private var onReselect: ((Int) -> Unit)? = null
    var selectedItemId: Int = R.id.nav_home
        set(value) {
            field = value
            renderSelection()
        }

    init {
        orientation = HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        setPadding(dp(4), dp(4), dp(4), dp(4))
        background = rounded(context.getColor(R.color.surface), dp(28).toFloat())
        elevation = dp(3).toFloat()
        outlineProvider = android.view.ViewOutlineProvider.BACKGROUND
        clipToOutline = true
        val menu = androidx.appcompat.widget.PopupMenu(context, this).menu
        MenuInflater(context).inflate(R.menu.bottom_nav, menu)
        for (index in 0 until menu.size()) {
            val item = menu.getItem(index)
            val tab = LinearLayout(context).apply {
                id = item.itemId
                orientation = VERTICAL
                gravity = Gravity.CENTER
                minimumHeight = dp(48)
                isFocusable = true
                contentDescription = item.title
                setPadding(dp(4), dp(2), dp(4), dp(2))
                addView(ImageView(context).apply {
                    setImageDrawable(item.icon)
                    importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
                }, LayoutParams(dp(24), dp(24)))
                addView(TextView(context).apply {
                    text = item.title
                    textSize = 12f
                    includeFontPadding = false
                    typeface = Typeface.create("sans-serif-medium", Typeface.NORMAL)
                    gravity = Gravity.CENTER
                    isSingleLine = true
                    importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
                }, LayoutParams(-1, -2).apply { topMargin = dp(1) })
                setOnClickListener {
                    if (selectedItemId == item.itemId) onReselect?.invoke(item.itemId)
                    else if (onSelect?.invoke(item.itemId) != false) selectedItemId = item.itemId
                }
            }
            items[item.itemId] = tab
            addView(tab, LayoutParams(0, -1, 1f))
        }
        renderSelection()
    }

    fun setOnItemSelectedListener(listener: (Int) -> Boolean) { onSelect = listener }
    fun setOnItemReselectedListener(listener: (Int) -> Unit) { onReselect = listener }

    override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
        val width = MeasureSpec.getSize(widthMeasureSpec).coerceAtMost(dp(328))
        val height = dp(56) + (dp(24) * (resources.configuration.fontScale - 1f).coerceAtLeast(0f)).toInt()
        super.onMeasure(MeasureSpec.makeMeasureSpec(width, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(height, MeasureSpec.EXACTLY))
    }

    private fun renderSelection() {
        val primary = context.getColor(R.color.primary)
        val muted = context.getColor(R.color.on_surface_variant)
        for ((id, tab) in items) {
            val selected = id == selectedItemId
            val color = if (selected) primary else muted
            tab.isSelected = selected
            ViewCompat.setStateDescription(tab, if (selected) context.getString(R.string.group_selected) else null)
            (tab.getChildAt(0) as ImageView).imageTintList = ColorStateList.valueOf(color)
            (tab.getChildAt(1) as TextView).setTextColor(color)
            tab.background = RippleDrawable(
                ColorStateList.valueOf(ColorUtils.setAlphaComponent(primary, 28)),
                rounded(if (selected) ColorUtils.setAlphaComponent(primary, 23) else android.graphics.Color.TRANSPARENT, dp(24).toFloat()),
                rounded(android.graphics.Color.WHITE, dp(24).toFloat()),
            )
        }
    }

    private fun rounded(color: Int, radius: Float) = GradientDrawable().apply { setColor(color); cornerRadius = radius }
    private fun dp(value: Int) = (value * resources.displayMetrics.density).toInt()
}
