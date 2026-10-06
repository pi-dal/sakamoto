package com.pidal.sakamoto.ui

import android.content.Context
import android.text.InputType
import android.view.View
import android.widget.LinearLayout
import androidx.core.widget.NestedScrollView
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.textfield.TextInputEditText
import com.google.android.material.textfield.TextInputLayout
import com.pidal.sakamoto.R

/** Dialogs keep invalid input visible and never report a failed save as success. */
object EditDialogs {
    fun text(context: Context, title: String, original: String, multiline: Boolean = false, secret: Boolean = false, save: (String) -> Unit) {
        val layout = TextInputLayout(context).apply { hint = title }
        val input = TextInputEditText(layout.context).apply {
            setText(original)
            inputType = InputType.TYPE_CLASS_TEXT or when { secret -> InputType.TYPE_TEXT_VARIATION_PASSWORD; multiline -> InputType.TYPE_TEXT_FLAG_MULTI_LINE; else -> InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS }
            isSingleLine = !multiline
            minLines = if (multiline) 8 else 1
            isSaveEnabled = !secret
            if (secret && android.os.Build.VERSION.SDK_INT >= 26) importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO
        }
        layout.addView(input, LinearLayout.LayoutParams(-1, -2))
        val root = LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            val padding = (20 * resources.displayMetrics.density).toInt()
            setPadding(padding, padding, padding, padding)
            addView(layout)
        }
        val scroll = NestedScrollView(context).apply { addView(root) }
        val dialog = MaterialAlertDialogBuilder(context).setTitle(title).setView(scroll)
            .setPositiveButton(R.string.save, null).setNegativeButton(android.R.string.cancel, null).create()
        dialog.setOnShowListener {
            dialog.getButton(android.app.AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                try { save(input.text.toString()); dialog.dismiss() }
                catch (error: Exception) { layout.error = error.message ?: context.getString(R.string.invalid_value) }
            }
        }
        dialog.setOnDismissListener { if (secret) input.setText("") }
        dialog.show()
    }

    fun choice(context: Context, title: String, options: List<String>, current: String, save: (String) -> Unit) {
        MaterialAlertDialogBuilder(context).setTitle(title).setSingleChoiceItems(options.toTypedArray(), options.indexOf(current)) { dialog, index ->
            try { save(options[index]); dialog.dismiss() }
            catch (error: Exception) {
                MaterialAlertDialogBuilder(context).setTitle(R.string.edit_failed).setMessage(error.message ?: context.getString(R.string.invalid_value)).setPositiveButton(android.R.string.ok, null).show()
            }
        }.setNegativeButton(android.R.string.cancel, null).show()
    }
}
