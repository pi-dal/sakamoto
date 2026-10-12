package com.pidal.sakamoto

import com.pidal.sakamoto.runtime.*
import org.junit.Assert.*
import org.junit.Test

class MockLocationRouteTest {
    private fun route() = MockLocationRoute(listOf(MockCoordinates(0.0,0.0), MockCoordinates(0.0,1.0), MockCoordinates(0.0,3.0)), 30)
    @Test fun followsDistanceWeightedLegsAndHoldsExactEndpoint() {
        val route = route().validate()
        assertEquals(1.0, route.position(10.0).longitude, 0.000001)
        assertEquals(2.0, route.position(20.0).longitude, 0.000001)
        assertEquals(route.points.last(), route.position(99.0))
        assertEquals(route.points.first(), route.position(-10.0))
        assertEquals(90f, route.bearing(5.0), 0.01f)
    }
    @Test fun crossesDateLineByShortArc() {
        val route = MockLocationRoute(listOf(MockCoordinates(0.0,179.0), MockCoordinates(0.0,-179.0)), 10).validate()
        assertTrue(route.distance < 230000)
        assertEquals(180.0, kotlin.math.abs(route.position(5.0).longitude), 0.000001)
    }
    @Test fun pauseResumePreservesElapsedAndCompletionDoesNotLoop() {
        val player = MockRoutePlayback(route(), 100.0)
        player.setPaused(true,110.0); assertEquals(10.0,player.elapsed(999.0),0.0)
        assertEquals(1.0, player.position(999.0).longitude,0.000001)
        player.setPaused(false,1000.0); assertEquals(20.0,player.elapsed(1010.0),0.0)
        assertEquals("Completed",player.state(1020.0))
        player.setPaused(true,1040.0); player.setPaused(false,1050.0)
        assertEquals(30.0,player.elapsed(1060.0),0.0)
    }
    @Test fun delayedTicksCatchUp() {
        val player = MockRoutePlayback(route(),100.0)
        assertEquals(0.0,player.elapsed(99.0),0.0)
        assertEquals(2.5,player.position(125.0).longitude,0.000001)
    }
    @Test fun rejectsInvalidPlans() {
        for (route in listOf(MockLocationRoute(emptyList(),10),MockLocationRoute(listOf(MockCoordinates(),MockCoordinates()),10),MockLocationRoute(listOf(MockCoordinates(),MockCoordinates(0.0,180.0)),10),MockLocationRoute(route().points,9),MockLocationRoute(route().points,30,0),MockLocationRoute(listOf(MockCoordinates(Double.NaN),MockCoordinates(1.0)),30))) {
            assertThrows(IllegalArgumentException::class.java) { route.validate() }
        }
        assertThrows(IllegalArgumentException::class.java) { MockLocationRoute.parse("0,0\n1,2,3","30","1",25) }
    }
    @Test fun parsesAndSerializesDraft() {
        val route = MockLocationRoute.parse(" 0, 0\n0, 1\n0, 3\n","30","1",15)
        assertEquals(route,MockLocationRoute.fromJSON(route.json()))
        assertEquals(15,route.position(5.0).accuracy)
    }
}
