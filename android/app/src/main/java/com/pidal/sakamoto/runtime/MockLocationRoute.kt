package com.pidal.sakamoto.runtime

import org.json.JSONArray
import org.json.JSONObject
import kotlin.math.*

/** Ordered WGS84 points, traversed at constant distance per second. No road routing service is involved. */
data class MockLocationRoute(val points: List<MockCoordinates>, val durationSeconds: Int, val intervalSeconds: Int = 1) {
    fun validate(): MockLocationRoute {
        require(points.size in 2..32 && durationSeconds in 10..86400 && intervalSeconds in 1..10) { "Use 2 to 32 route points, 10 to 86400 seconds and an interval of 1 to 10 seconds" }
        points.forEach { it.validate() }
        points.zipWithNext().forEach { (a, b) -> require(distance(a, b) >= 0.01 && distance(a, b) < EARTH * (PI - 0.00001)) { "Adjacent route points must differ; add an intermediate point for opposite sides of Earth" } }
        return this
    }
    val distance: Double get() = points.zipWithNext().sumOf { (a, b) -> distance(a, b) }
    fun position(elapsed: Double): MockCoordinates {
        val progress = (elapsed / durationSeconds).coerceIn(0.0, 1.0)
        if (progress <= 0) return points.first(); if (progress >= 1) return points.last()
        var remaining = distance * progress
        for ((a, b) in points.zipWithNext()) {
            val leg = distance(a, b)
            if (remaining <= leg) return interpolate(a, b, remaining / leg)
            remaining -= leg
        }
        return points.last()
    }
    fun bearing(elapsed: Double): Float {
        if (elapsed >= durationSeconds) return 0f
        val a = position(elapsed); val b = position(min(durationSeconds.toDouble(), elapsed + 0.1))
        val lat = Math.toRadians(a.latitude); val next = Math.toRadians(b.latitude); val d = Math.toRadians(b.longitude - a.longitude)
        return ((Math.toDegrees(atan2(sin(d) * cos(next), cos(lat) * sin(next) - sin(lat) * cos(next) * cos(d))) + 360) % 360).toFloat()
    }
    fun json(): JSONObject = JSONObject().put("points", JSONArray().also { array -> points.forEach { array.put(it.json()) } }).put("durationSeconds", durationSeconds).put("intervalSeconds", intervalSeconds)
    companion object {
        private const val EARTH = 6371000.0
        fun fromJSON(value: JSONObject): MockLocationRoute {
            val array = value.getJSONArray("points"); require(array.length() in 2..32) { "Use 2 to 32 route points" }
            return MockLocationRoute((0 until array.length()).map { MockCoordinates.fromJSON(array.getJSONObject(it)) }, value.getInt("durationSeconds"), value.getInt("intervalSeconds")).validate()
        }
        fun parse(text: String, duration: String, interval: String, accuracy: Int): MockLocationRoute {
            require(text.length <= 4096) { "Route text is too large" }
            val lines = text.lines().filter { it.isNotBlank() }; require(lines.size in 2..32) { "Enter 2 to 32 lines, each containing latitude, longitude" }
            val points = lines.map {
                val values = it.split(','); require(values.size == 2) { "Each route line must contain decimal latitude, longitude" }
                MockCoordinates.parse(values[0], values[1], accuracy.toString())
            }
            return MockLocationRoute(points, duration.trim().toIntOrNull() ?: error("Enter a whole-number duration"), interval.trim().toIntOrNull() ?: error("Enter a whole-number interval")).validate()
        }
        fun distance(a: MockCoordinates, b: MockCoordinates): Double {
            val lat = Math.toRadians(a.latitude); val next = Math.toRadians(b.latitude)
            val d = sin((next - lat) / 2); val e = sin(Math.toRadians(b.longitude - a.longitude) / 2)
            return EARTH * 2 * asin(sqrt((d*d + cos(lat)*cos(next)*e*e).coerceIn(0.0, 1.0)))
        }
        private fun interpolate(a: MockCoordinates, b: MockCoordinates, fraction: Double): MockCoordinates {
            val angle = distance(a, b) / EARTH
            val first = sin((1-fraction)*angle)/sin(angle); val second = sin(fraction*angle)/sin(angle)
            fun vector(p: MockCoordinates): DoubleArray { val lat = Math.toRadians(p.latitude); val lon = Math.toRadians(p.longitude); return doubleArrayOf(cos(lat)*cos(lon), cos(lat)*sin(lon), sin(lat)) }
            val start = vector(a); val end = vector(b); val v = DoubleArray(3) { first*start[it]+second*end[it] }
            return MockCoordinates(Math.toDegrees(atan2(v[2], hypot(v[0],v[1]))), Math.toDegrees(atan2(v[1],v[0])), a.accuracy)
        }
    }
}

class MockRoutePlayback(val route: MockLocationRoute, now: Double) {
    private var accumulated = 0.0
    private var started = now
    var paused = false; private set
    init { route.validate() }
    fun elapsed(now: Double) = min(route.durationSeconds.toDouble(), accumulated + if (paused) 0.0 else max(0.0, now-started))
    fun state(now: Double) = if (elapsed(now) >= route.durationSeconds) "Completed" else if (paused) "Paused" else "Moving"
    fun position(now: Double) = route.position(elapsed(now))
    fun setPaused(value: Boolean, now: Double) { accumulated = elapsed(now); started = now; paused = value }
}
