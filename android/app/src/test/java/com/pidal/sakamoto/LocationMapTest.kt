package com.pidal.sakamoto

import com.pidal.sakamoto.runtime.LocationMapDraft
import com.pidal.sakamoto.runtime.LocationMapGeometry
import com.pidal.sakamoto.runtime.LocationPlaceSearch
import com.pidal.sakamoto.runtime.MockCoordinates
import org.junit.Assert.*
import org.junit.Test

class LocationMapTest {
    @Test fun editingAnIndependentDraftCannotChangeItsInput() {
        val original = listOf(MockCoordinates(37.3, -122.0), MockCoordinates(37.4, -122.1))
        val draft = LocationMapDraft(true, original)
        draft.undo(); draft.choose(MockCoordinates(37.5, -122.2))
        assertEquals(37.4, original.last().latitude, 0.0)
        assertEquals(37.5, draft.commit().last().latitude, 0.0)
        draft.clear()
        assertThrows(IllegalArgumentException::class.java) { draft.commit() }
        assertEquals(2, original.size)
    }
    @Test fun rejectedStopsLeaveTheDraftIntact() {
        val draft = LocationMapDraft(true, listOf(MockCoordinates(0.0, 0.0)))
        assertThrows(IllegalArgumentException::class.java) { draft.choose(MockCoordinates(0.0, 0.0)) }
        assertThrows(IllegalArgumentException::class.java) { draft.choose(MockCoordinates(Double.NaN, 0.0)) }
        assertEquals(1, draft.points.size)
        for (index in 1..31) draft.choose(MockCoordinates(0.0, index.toDouble()))
        assertThrows(IllegalArgumentException::class.java) { draft.choose(MockCoordinates(0.0, 32.0)) }
        assertEquals(32, draft.commit().size)
    }
    @Test fun choosingAPositionReplacesItInsteadOfAppending() {
        val draft = LocationMapDraft(false, listOf(MockCoordinates(0.0, 0.0)))
        draft.choose(MockCoordinates(22.5, 114.0))
        assertEquals(listOf(MockCoordinates(22.5, 114.0)), draft.commit())
    }
    @Test fun mapPathCrossesTheDateLineInBothDirectionsWithoutCrossingTheWorld() {
        val points = listOf(MockCoordinates(10.0, 179.0), MockCoordinates(11.0, -179.0))
        for (ordered in listOf(points, points.reversed())) {
            val segments = LocationMapGeometry.segments(ordered)
            assertEquals(2, segments.size)
            assertEquals(ordered.first(), segments.first().first())
            assertEquals(ordered.last(), segments.last().last())
            assertEquals(180.0, kotlin.math.abs(segments.first().last().longitude), 0.0)
            assertEquals(-segments.first().last().longitude, segments.last().first().longitude, 0.0)
            assertTrue(segments.all { leg -> leg.zipWithNext().all { (a, b) -> kotlin.math.abs(a.longitude - b.longitude) <= 180 } })
        }
    }
    @Test fun searchCoordinatesUseLongitudeFirstAndRejectBadGeometry() {
        val raw = """{"features":[
          {"geometry":{"type":"Point","coordinates":[114.05,22.55]},"properties":{"name":"Park","city":"Shenzhen","country":"China"}},
          {"geometry":{"type":"Point","coordinates":[114.05,122.55]},"properties":{"name":"Invalid latitude"}},
          {"geometry":{"type":"Polygon","coordinates":[]},"properties":{"name":"Wrong geometry"}}
        ]}"""
        val results = LocationPlaceSearch.decode(raw)
        assertEquals(1, results.size)
        assertEquals(MockCoordinates(22.55, 114.05), results.single().point)
        assertEquals("Shenzhen, China", results.single().detail)
    }
}
