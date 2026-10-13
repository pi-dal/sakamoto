package com.pidal.sakamoto.ui

/** Editors share the same guard when navigation leaves unfinished changes. */
interface UnsavedConfigEditor {
    fun hasUnsavedChanges(): Boolean
    fun confirmLeaving(action: () -> Unit)
}
