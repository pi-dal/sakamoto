package com.pidal.sakamoto.ui

import android.app.Application
import android.content.ClipData
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.LinearLayout
import androidx.core.content.FileProvider
import androidx.fragment.app.Fragment
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewModelScope
import com.google.android.material.progressindicator.LinearProgressIndicator
import com.pidal.sakamoto.BuildConfig
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.AndroidUpdate
import com.pidal.sakamoto.runtime.AndroidUpdateRelease
import com.pidal.sakamoto.runtime.AppUpdateRepository
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.io.File
import java.io.IOException

internal enum class UpdatePhase { IDLE, CHECKING, AVAILABLE, LATEST, DOWNLOADING, VERIFYING, READY, FAILED, CANCELLING }
internal data class UpdateScreenState(val phase: UpdatePhase = UpdatePhase.IDLE, val update: AndroidUpdate? = null, val received: Long = 0, val message: String? = null) {
    val busy get() = phase in setOf(UpdatePhase.CHECKING, UpdatePhase.DOWNLOADING, UpdatePhase.VERIFYING, UpdatePhase.CANCELLING)
}

/** Rotation retains the operation. Leaving this destination cancels unfinished work. */
class AppUpdateModel(application: Application) : AndroidViewModel(application) {
    private val repository = AppUpdateRepository(application)
    private val mutableState = MutableStateFlow(UpdateScreenState())
    internal val state: StateFlow<UpdateScreenState> = mutableState
    private var operation: Job? = null
    companion object { private val cacheLock = Mutex() }
    init {
        run(UpdatePhase.VERIFYING) {
            val saved = repository.restore()
            mutableState.value = UpdateScreenState(if (saved == null) UpdatePhase.IDLE else UpdatePhase.READY, saved)
        }
    }
    fun check() = run(UpdatePhase.CHECKING, keepUpdate = false) {
        val update = repository.check()
        mutableState.value = UpdateScreenState(if (update == null) UpdatePhase.LATEST else UpdatePhase.AVAILABLE, update)
    }
    fun download() {
        val update = mutableState.value.update ?: return
        run(UpdatePhase.DOWNLOADING) {
            repository.download(update) { received ->
                mutableState.value = UpdateScreenState(if (received == update.bytes) UpdatePhase.VERIFYING else UpdatePhase.DOWNLOADING, update, received)
            }
            mutableState.value = UpdateScreenState(UpdatePhase.READY, update)
        }
    }
    fun install(ready: (File) -> Unit) {
        val update = mutableState.value.update ?: return
        run(UpdatePhase.VERIFYING) {
            val file = repository.verifiedInstaller(update)
            withContext(Dispatchers.Main) {
                mutableState.value = UpdateScreenState(UpdatePhase.READY, update)
                ready(file)
            }
        }
    }
    fun notice(message: String) { mutableState.value = mutableState.value.copy(message = message) }
    fun cancel() {
        val active = operation ?: return
        mutableState.value = mutableState.value.copy(phase = UpdatePhase.CANCELLING)
        active.cancel()
        repository.cancel()
        viewModelScope.launch {
            active.join()
            val saved = mutableState.value.update
            mutableState.value = UpdateScreenState(if (saved == null) UpdatePhase.IDLE else UpdatePhase.AVAILABLE, saved)
        }
    }
    private fun run(phase: UpdatePhase, keepUpdate: Boolean = true, block: suspend () -> Unit) {
        if (operation?.isActive == true || mutableState.value.phase == UpdatePhase.CANCELLING) return
        val saved = mutableState.value.update.takeIf { keepUpdate }
        mutableState.value = UpdateScreenState(phase, saved)
        operation = viewModelScope.launch {
            try { withContext(Dispatchers.IO) { cacheLock.withLock { block() } } }
            catch (error: CancellationException) { throw error }
            catch (error: Exception) {
                val message = when (error) {
                    is IOException -> if (error.message?.startsWith("GitHub") == true) error.message else getApplication<Application>().getString(R.string.update_network_error)
                    is IllegalArgumentException, is IllegalStateException -> error.message
                    else -> getApplication<Application>().getString(R.string.update_invalid_response)
                }
                mutableState.value = UpdateScreenState(UpdatePhase.FAILED, saved, message = message)
            }
        }
    }
    override fun onCleared() { repository.cancel(); super.onCleared() }
}

class AppUpdateFragment : Fragment() {
    private lateinit var model: AppUpdateModel
    private var render: ((UpdateScreenState) -> Unit)? = null
    override fun onCreate(state: Bundle?) { super.onCreate(state); model = ViewModelProvider(this)[AppUpdateModel::class.java] }
    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, state: Bundle?): View {
        val page = GroupedPage(requireContext())
        val installed = page.section(getString(R.string.update_installed))
        page.row(installed, getString(R.string.about_version_label), "${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})", navigates = false)
        val status = page.section(getString(R.string.app_updates))
        val statusText = page.paragraph(status, getString(R.string.update_idle))
        statusText.accessibilityLiveRegion = View.ACCESSIBILITY_LIVE_REGION_POLITE
        val progress = LinearProgressIndicator(requireContext()).apply { max = 1000; visibility = View.GONE }
        status.addView(progress, LinearLayout.LayoutParams(-1, -2).apply { setMargins(page.dp(20), page.dp(8), page.dp(20), page.dp(8)) })
        val detail = page.paragraph(status, "").apply { visibility = View.GONE }
        val primary = page.button(status, getString(R.string.update_check), primary = true) {
            when (model.state.value.phase) {
                UpdatePhase.AVAILABLE -> model.download()
                UpdatePhase.READY -> model.install { file ->
                    if (isAdded && view != null && lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) openInstaller(file)
                }
                UpdatePhase.FAILED -> if (model.state.value.update != null) model.download() else model.check()
                else -> model.check()
            }
        }
        val cancel = page.button(status, getString(R.string.update_cancel)) { model.cancel() }.apply { visibility = View.GONE }
        val again = page.button(status, getString(R.string.update_check_again)) { model.check() }.apply { visibility = View.GONE }
        page.note(getString(R.string.update_download_note))
        val source = page.section(getString(R.string.update_source))
        page.setting(source, getString(R.string.update_github), icon = R.drawable.ic_info) {
            openURL(model.state.value.update?.release?.pageURL ?: "${AndroidUpdateRelease.REPOSITORY}/releases")
        }
        val releaseNotes = page.section(getString(R.string.update_release_notes)).apply { visibility = View.GONE }
        val notes = page.paragraph(releaseNotes, "")
        render = { value ->
                        val update = value.update
                        statusText.text = getString(when (value.phase) {
                            UpdatePhase.IDLE -> R.string.update_idle
                            UpdatePhase.CHECKING -> R.string.update_checking
                            UpdatePhase.AVAILABLE -> R.string.update_available
                            UpdatePhase.LATEST -> R.string.update_latest
                            UpdatePhase.DOWNLOADING -> R.string.update_downloading
                            UpdatePhase.VERIFYING -> R.string.update_verifying
                            UpdatePhase.READY -> R.string.update_ready
                            UpdatePhase.FAILED -> R.string.update_failed
                            UpdatePhase.CANCELLING -> R.string.update_cancelling
                        })
                        detail.visibility = if (value.message != null || update != null) View.VISIBLE else View.GONE
                        detail.text = value.message ?: update?.let {
                            val size = android.text.format.Formatter.formatShortFileSize(requireContext(), it.bytes)
                            if (value.phase == UpdatePhase.DOWNLOADING) getString(R.string.update_progress, android.text.format.Formatter.formatShortFileSize(requireContext(), value.received), size)
                            else "${it.release.versionName} (${it.versionCode}) · ${it.abi} · $size"
                        }.orEmpty()
                        primary.isEnabled = !value.busy
                        primary.text = getString(when {
                            value.phase == UpdatePhase.DOWNLOADING -> R.string.update_downloading
                            value.phase == UpdatePhase.VERIFYING -> R.string.update_verifying
                            value.phase == UpdatePhase.CHECKING -> R.string.update_checking
                            value.phase == UpdatePhase.CANCELLING -> R.string.update_cancelling
                            value.phase == UpdatePhase.AVAILABLE -> R.string.update_download
                            value.phase == UpdatePhase.READY -> R.string.update_install
                            value.phase == UpdatePhase.FAILED && update != null -> R.string.update_retry_download
                            value.phase == UpdatePhase.FAILED -> R.string.update_retry_check
                            else -> R.string.update_check
                        })
                        cancel.visibility = if (value.phase in setOf(UpdatePhase.DOWNLOADING, UpdatePhase.CHECKING)) View.VISIBLE else View.GONE
                        again.visibility = if (!value.busy && update != null) View.VISIBLE else View.GONE
                        val indeterminate = value.phase != UpdatePhase.DOWNLOADING
                        if (progress.isIndeterminate != indeterminate) {
                            progress.visibility = View.GONE
                            progress.isIndeterminate = indeterminate
                        }
                        progress.visibility = if (value.busy) View.VISIBLE else View.GONE
                        if (value.phase == UpdatePhase.DOWNLOADING && update != null) progress.progress = (value.received * 1000 / update.bytes).toInt()
                        releaseNotes.visibility = if (update?.release?.notes.isNullOrBlank()) View.GONE else View.VISIBLE
                        notes.text = update?.release?.notes.orEmpty()
        }
        return page.root
    }
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        viewLifecycleOwner.lifecycleScope.launch {
            viewLifecycleOwner.repeatOnLifecycle(Lifecycle.State.STARTED) { model.state.collect { render?.invoke(it) } }
        }
    }
    override fun onDestroyView() { render = null; super.onDestroyView() }
    private fun openInstaller(file: File) {
        try {
            if (Build.VERSION.SDK_INT >= 26 && !requireContext().packageManager.canRequestPackageInstalls()) {
                model.notice(getString(R.string.update_allow_install))
                startActivity(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${requireContext().packageName}")))
                return
            }
            val uri = FileProvider.getUriForFile(requireContext(), "${requireContext().packageName}.updates", file)
            startActivity(Intent(Intent.ACTION_VIEW).setDataAndType(uri, "application/vnd.android.package-archive")
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION).apply { clipData = ClipData.newRawUri("Sakamoto update", uri) })
            model.notice(getString(R.string.update_installer_opened))
        } catch (_: Exception) { model.notice(getString(R.string.update_installer_error)) }
    }
    private fun openURL(url: String) {
        try { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url))) }
        catch (_: Exception) { model.notice(getString(R.string.update_browser_error)) }
    }
}
