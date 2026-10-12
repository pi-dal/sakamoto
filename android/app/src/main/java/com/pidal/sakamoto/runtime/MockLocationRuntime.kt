package com.pidal.sakamoto.runtime

import android.app.AppOpsManager
import android.content.Context
import android.os.Build
import android.os.Process
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import org.json.JSONArray
import org.json.JSONObject

/** Decimal WGS84 coordinates. Requested state is never restored at boot. */
data class MockCoordinates(val latitude: Double = 0.0, val longitude: Double = 0.0, val accuracy: Int = 25) {
    fun validate(): MockCoordinates {
        require(latitude.isFinite() && latitude in -90.0..90.0 && longitude.isFinite() && longitude in -180.0..180.0) { "Latitude must be -90 to 90; longitude must be -180 to 180" }
        require(accuracy in 1..1000) { "Accuracy must be 1 to 1000 meters" }
        return this
    }
    fun json() = JSONObject().put("latitude", latitude).put("longitude", longitude).put("accuracy", accuracy)
    companion object {
        fun parse(latitude: String, longitude: String, accuracy: String) = MockCoordinates(
            latitude.trim().replace(',', '.').toDoubleOrNull() ?: throw IllegalArgumentException("Enter a decimal latitude"),
            longitude.trim().replace(',', '.').toDoubleOrNull() ?: throw IllegalArgumentException("Enter a decimal longitude"),
            accuracy.trim().toIntOrNull() ?: throw IllegalArgumentException("Enter a whole-number accuracy")
        ).validate()
        fun fromJSON(value: JSONObject) = MockCoordinates(value.getDouble("latitude"), value.getDouble("longitude"), value.getInt("accuracy")).validate()
    }
}

data class FavoriteLocation(val name: String, val point: MockCoordinates)
data class MockLocationSnapshot(val phase: String = "Off", val fixes: Long = 0, val lastFix: Long = 0, val fused: Boolean = false, val error: String = "", val recoveryRequired: Boolean = false)

object MockLocationRuntime {
    private val mutable = MutableStateFlow(MockLocationSnapshot())
    val snapshot = mutable.asStateFlow()
    fun update(value: MockLocationSnapshot) { mutable.value = value }
    fun authorized(context: Context): Boolean = runCatching {
        val ops = context.getSystemService(AppOpsManager::class.java)
        val mode = if (Build.VERSION.SDK_INT >= 29) ops.unsafeCheckOpNoThrow(AppOpsManager.OPSTR_MOCK_LOCATION, Process.myUid(), context.packageName)
        else ops.checkOpNoThrow(AppOpsManager.OPSTR_MOCK_LOCATION, Process.myUid(), context.packageName)
        mode == AppOpsManager.MODE_ALLOWED
    }.getOrDefault(false)
}

class MockLocationStore(context: Context) {
    private val prefs = context.getSharedPreferences("virtual-location", Context.MODE_PRIVATE)
    fun needsRecovery() = prefs.getBoolean("cleanupPending", false)
    fun markRecovery(pending: Boolean) { prefs.edit().putBoolean("cleanupPending", pending).apply() }
    fun load(): MockCoordinates = runCatching { MockCoordinates.fromJSON(JSONObject(prefs.getString("point", "")!!)) }.getOrDefault(MockCoordinates())
    fun save(point: MockCoordinates) { prefs.edit().putString("point", point.validate().json().toString()).apply() }
    fun favorites(): List<FavoriteLocation> = runCatching {
        val values = JSONArray(prefs.getString("favorites", "[]"))
        (0 until minOf(values.length(), 20)).mapNotNull { index -> runCatching {
            val value = values.getJSONObject(index)
            FavoriteLocation(value.getString("name").take(64), MockCoordinates.fromJSON(value.getJSONObject("point")))
        }.getOrNull() }
    }.getOrDefault(emptyList())
    fun saveFavorite(name: String, point: MockCoordinates) {
        val label = name.trim().take(64); require(label.isNotEmpty()) { "Give this location a name" }; point.validate()
        saveFavorites(listOf(FavoriteLocation(label, point)) + favorites().filterNot { it.name == label })
    }
    fun removeFavorite(name: String) = saveFavorites(favorites().filterNot { it.name == name })
    private fun saveFavorites(values: List<FavoriteLocation>) {
        val array = JSONArray(); values.take(20).forEach { array.put(JSONObject().put("name", it.name).put("point", it.point.json())) }
        prefs.edit().putString("favorites", array.toString()).apply()
    }
}
