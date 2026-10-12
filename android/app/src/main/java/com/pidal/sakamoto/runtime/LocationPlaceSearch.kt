package com.pidal.sakamoto.runtime

import android.os.SystemClock
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.delay
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import okhttp3.Call
import okhttp3.Callback
import okhttp3.Response
import java.io.IOException
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/** Submit-only Photon queries, serialized and cached across page re-entry. No location access. */
object LocationPlaceSearch {
    data class Place(val name: String, val detail: String, val point: MockCoordinates)
    private val client = OkHttpClient.Builder().connectTimeout(8, TimeUnit.SECONDS).readTimeout(8, TimeUnit.SECONDS).callTimeout(12, TimeUnit.SECONDS).build()
    private val gate = Mutex()
    private val cache = LinkedHashMap<String, List<Place>>()
    private var lastRequest = -1000L
    suspend fun search(query: String): List<Place> = gate.withLock {
        val text = query.trim().take(200)
        require(text.isNotEmpty()) { "Enter a place or address" }
        cache[text]?.let { return@withLock it }
        val wait = 1000 - (SystemClock.elapsedRealtime() - lastRequest)
        if (wait > 0) delay(wait)
        lastRequest = SystemClock.elapsedRealtime()
        val url = "https://photon.komoot.io/api/".toHttpUrl().newBuilder().addQueryParameter("q", text).addQueryParameter("limit", "5").build()
        val request = Request.Builder().url(url).header("User-Agent", "Sakamoto Android/1 (+https://github.com/pi-dal/sakamoto)").build()
        val values = suspendCancellableCoroutine<List<Place>> { continuation ->
            val call = client.newCall(request)
            continuation.invokeOnCancellation { call.cancel() }
            call.enqueue(object : Callback {
                override fun onFailure(call: Call, error: IOException) { continuation.resumeWithException(error) }
                override fun onResponse(call: Call, response: Response) {
                    try {
                        val result = response.use {
                            check(it.isSuccessful) { "Place search is unavailable. Try again later or select on the map." }
                            val source = requireNotNull(it.body).source()
                            // Never allocate an unbounded response from a public geocoder.
                            check(!source.request(262145)) { "Place search returned too much data. Try a more specific address." }
                            decode(source.readUtf8())
                        }
                        continuation.resume(result)
                    } catch (error: Exception) { continuation.resumeWithException(error) }
                }
            })
        }
        if (cache.size >= 50) cache.remove(cache.keys.first())
        cache[text] = values
        values
    }
    internal fun decode(raw: String): List<Place> {
        val features = JSONObject(raw).getJSONArray("features")
        return (0 until minOf(5, features.length())).mapNotNull { index -> runCatching {
            val feature = features.getJSONObject(index)
            val geometry = feature.getJSONObject("geometry")
            require(geometry.getString("type") == "Point")
            val coordinates = geometry.getJSONArray("coordinates")
            val point = MockCoordinates(coordinates.getDouble(1), coordinates.getDouble(0)).validate()
            val properties = feature.getJSONObject("properties")
            val detail = listOf("street", "city", "state", "country").map { properties.optString(it) }.filter { it.isNotBlank() }.distinct().joinToString(", ")
            Place(properties.optString("name").ifBlank { detail.ifBlank { "Place" } }, detail, point)
        }.getOrNull() }
    }
}
