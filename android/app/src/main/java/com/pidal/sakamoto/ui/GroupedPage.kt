package com.pidal.sakamoto.ui

import android.content.Context
import android.text.InputType
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import androidx.appcompat.content.res.AppCompatResources
import androidx.core.view.ViewCompat
import androidx.core.widget.NestedScrollView
import com.google.android.material.button.MaterialButton
import com.google.android.material.materialswitch.MaterialSwitch
import com.google.android.material.textfield.TextInputEditText
import com.google.android.material.textfield.TextInputLayout
import com.pidal.sakamoto.R

/** Shared native list grammar: section -> surface rows -> group-outside explanation.
 * Rows grow with font scale; no fixed-height cells, fake cards or stock platform buttons. */
class GroupedPage(val context: Context) {
    val root = NestedScrollView(context).apply {
        setBackgroundColor(context.getColor(R.color.surface_variant))
        isFillViewport = true
    }
    private val content = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(0, 0, 0, dp(24))
    }
    init { root.addView(content, ViewGroup.LayoutParams(-1, -2)) }
    fun dp(value: Int) = (value * context.resources.displayMetrics.density).toInt()
    fun text(value: CharSequence, appearance: Int = com.google.android.material.R.style.TextAppearance_Material3_BodyMedium): TextView = TextView(context).apply {
        setTextAppearance(appearance)
        text = value
        setTextColor(context.getColor(R.color.on_surface))
    }
    fun section(title: CharSequence): LinearLayout {
        content.addView(text(title, com.google.android.material.R.style.TextAppearance_Material3_TitleSmall).apply {
            setTextColor(context.getColor(R.color.primary))
            setPadding(dp(20), dp(24), dp(20), dp(8))
            ViewCompat.setAccessibilityHeading(this, true)
        })
        return LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(context.getColor(R.color.surface))
            content.addView(this, LinearLayout.LayoutParams(-1, -2))
        }
    }
    fun note(value: CharSequence): TextView = text(value, com.google.android.material.R.style.TextAppearance_Material3_BodySmall).apply {
        setTextColor(context.getColor(R.color.on_surface_variant))
        setPadding(dp(20), dp(12), dp(20), dp(4))
        content.addView(this, LinearLayout.LayoutParams(-1, -2))
    }
    fun paragraph(group: LinearLayout, value: CharSequence): TextView = text(value).apply {
        setPadding(dp(20), dp(16), dp(20), dp(16))
        setTextIsSelectable(true)
        group.addView(this, LinearLayout.LayoutParams(-1, -2))
    }
    fun divider(group: LinearLayout) {
        group.addView(View(context).apply { setBackgroundColor(context.getColor(R.color.divider)) }, LinearLayout.LayoutParams(-1, dp(1)).apply { marginStart = dp(20) })
    }
    fun row(group: LinearLayout, title: CharSequence, detail: CharSequence = "", icon: Int = 0, navigates: Boolean = true, action: (() -> Unit)? = null): Row {
        if (group.childCount > 0) divider(group)
        val row = Row(context, title, detail, icon, navigates, action)
        group.addView(row, LinearLayout.LayoutParams(-1, -2))
        return row
    }
    fun toggle(group: LinearLayout, title: CharSequence, checked: Boolean): MaterialSwitch {
        val row = row(group, title)
        val control = MaterialSwitch(context).apply {
            isChecked = checked
            contentDescription = title
            minHeight = dp(48)
        }
        row.trailing(control)
        return control
    }
    fun field(group: LinearLayout, label: String, value: String = "", secret: Boolean = false, uri: Boolean = false): TextInputEditText {
        val layout = TextInputLayout(context).apply {
            hint = label
            boxBackgroundMode = TextInputLayout.BOX_BACKGROUND_OUTLINE
            if (secret) endIconMode = TextInputLayout.END_ICON_PASSWORD_TOGGLE
        }
        val input = TextInputEditText(layout.context).apply {
            setText(value)
            isSingleLine = true
            inputType = InputType.TYPE_CLASS_TEXT or when {
                secret -> InputType.TYPE_TEXT_VARIATION_PASSWORD
                uri -> InputType.TYPE_TEXT_VARIATION_URI
                else -> InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
            }
            if (secret) {
                isSaveEnabled = false
                if (android.os.Build.VERSION.SDK_INT >= 26) importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO
            }
        }
        layout.addView(input, LinearLayout.LayoutParams(-1, -2))
        group.addView(layout, LinearLayout.LayoutParams(-1, -2).apply {
            marginStart = dp(20); marginEnd = dp(20); topMargin = dp(12); bottomMargin = dp(4)
        })
        return input
    }
    fun button(group: LinearLayout, label: CharSequence, primary: Boolean = false, action: () -> Unit): MaterialButton {
        val style = if (primary) com.google.android.material.R.attr.materialButtonStyle else com.google.android.material.R.attr.borderlessButtonStyle
        return MaterialButton(context, null, style).apply {
            text = label
            minHeight = dp(48)
            setOnClickListener { action() }
            group.addView(this, LinearLayout.LayoutParams(-1, -2).apply {
                marginStart = dp(20); marginEnd = dp(20); topMargin = dp(8); bottomMargin = dp(12)
            })
        }
    }

    class Row(context: Context, title: CharSequence, detail: CharSequence, icon: Int, navigates: Boolean, action: (() -> Unit)?) : LinearLayout(context) {
        val label = TextView(context)
        val value = TextView(context)
        private val leading = ImageView(context)
        private val endActions = LinearLayout(context).apply {
            orientation = HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL or Gravity.END
        }
        private val chevron = ImageView(context).apply {
            setImageResource(R.drawable.ic_chevron)
            imageTintList = android.content.res.ColorStateList.valueOf(context.getColor(R.color.on_surface_variant))
            importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
            if (resources.configuration.layoutDirection == View.LAYOUT_DIRECTION_RTL) scaleX = -1f
        }
        private fun dp(value: Int) = (value * resources.displayMetrics.density).toInt()
        init {
            orientation = HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            minimumHeight = dp(if (detail.isEmpty()) 56 else 72)
            setPaddingRelative(dp(20), dp(12), dp(12), dp(12))
            leading.setImageResource(icon)
            leading.imageTintList = android.content.res.ColorStateList.valueOf(context.getColor(R.color.on_surface_variant))
            leading.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
            leading.visibility = if (icon == 0) View.GONE else View.VISIBLE
            addView(leading, LayoutParams(dp(24), dp(24)).apply { marginEnd = dp(16) })
            val labels = LinearLayout(context).apply { orientation = VERTICAL }
            label.setTextAppearance(com.google.android.material.R.style.TextAppearance_Material3_BodyLarge)
            label.setTextColor(context.getColor(R.color.on_surface)); label.text = title
            value.setTextAppearance(com.google.android.material.R.style.TextAppearance_Material3_BodySmall)
            value.setTextColor(context.getColor(R.color.on_surface_variant)); value.text = detail
            value.visibility = if (detail.isEmpty()) View.GONE else View.VISIBLE
            labels.addView(label, LayoutParams(-1, -2))
            labels.addView(value, LayoutParams(-1, -2).apply { topMargin = dp(4) })
            addView(labels, LayoutParams(0, -2, 1f))
            addView(endActions, LayoutParams(-2, -2).apply { marginStart = dp(8) })
            if (action != null) {
                val attr = android.util.TypedValue()
                context.theme.resolveAttribute(android.R.attr.selectableItemBackground, attr, true)
                foreground = AppCompatResources.getDrawable(context, attr.resourceId)
                isFocusable = true
                setOnClickListener { action() }
                if (navigates) endActions.addView(chevron, LayoutParams(dp(24), dp(24)).apply { marginEnd = dp(8) })
            }
        }

        /** A fixed 48dp action sits at the logical end; the title consumes the remainder. */
        fun actionIcon(icon: Int, description: String, action: (View) -> Unit): View {
            endActions.removeView(chevron)
            val button = androidx.appcompat.widget.AppCompatImageButton(context).apply {
                setImageResource(icon)
                imageTintList = android.content.res.ColorStateList.valueOf(context.getColor(R.color.on_surface_variant))
                scaleType = ImageView.ScaleType.CENTER
                contentDescription = description
                val attr = android.util.TypedValue()
                context.theme.resolveAttribute(android.R.attr.selectableItemBackgroundBorderless, attr, true)
                background = AppCompatResources.getDrawable(context, attr.resourceId)
                setOnClickListener { action(it) }
            }
            endActions.addView(button, LayoutParams(dp(48), dp(48)).apply {
                if (endActions.childCount > 0) marginStart = dp(8)
            })
            return button
        }
        fun trailing(control: View) {
            endActions.removeView(chevron)
            endActions.addView(control, LayoutParams(-2, -2))
        }
        fun selectedNode(selected: Boolean) {
            leading.setImageResource(if (selected) R.drawable.ic_selected else R.drawable.ic_node)
            leading.imageTintList = android.content.res.ColorStateList.valueOf(context.getColor(if (selected) R.color.primary else R.color.on_surface_variant))
            label.setTextColor(context.getColor(if (selected) R.color.primary else R.color.on_surface))
            ViewCompat.setStateDescription(this, if (selected) context.getString(R.string.group_selected) else context.getString(R.string.profile_not_selected))
        }
        fun detail(text: CharSequence) {
            value.text = text
            value.visibility = if (text.isEmpty()) View.GONE else View.VISIBLE
        }
    }
}
