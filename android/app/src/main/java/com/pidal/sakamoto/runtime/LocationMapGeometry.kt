package com.pidal.sakamoto.runtime

/** Sample playback arcs and cut at the date line so a map never draws a leg across the wrong hemisphere. */
object LocationMapGeometry {
    fun segments(points: List<MockCoordinates>): List<List<MockCoordinates>> {
        if (points.size < 2) return emptyList()
        val samples = points.zipWithNext().flatMap { (a, b) ->
            val leg = MockLocationRoute(listOf(a, b), 300).validate()
            (0..16).map { leg.position(it * 300.0 / 16) }
        }
        val result = mutableListOf<List<MockCoordinates>>()
        var segment = mutableListOf(samples.first())
        for (next in samples.drop(1)) {
            val previous = segment.last()
            val delta = next.longitude - previous.longitude
            if (delta > 180 || delta < -180) {
                val edge = if (delta > 180) -180.0 else 180.0
                val unwrapped = next.longitude + if (delta > 180) -360 else 360
                val fraction = (edge - previous.longitude) / (unwrapped - previous.longitude)
                val latitude = previous.latitude + fraction * (next.latitude - previous.latitude)
                segment.add(MockCoordinates(latitude, edge))
                if (segment.any { it != segment.first() }) result.add(segment)
                segment = mutableListOf(MockCoordinates(latitude, -edge))
            }
            segment.add(next)
        }
        if (segment.size >= 2 && segment.any { it != segment.first() }) result.add(segment)
        return result
    }
}
