package com.pidal.sakamoto

import com.pidal.sakamoto.runtime.MockCoordinates
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class MockCoordinatesTest {
    @Test fun acceptsSignedDecimalCoordinatesAndBoundaries() {
        assertEquals(MockCoordinates(-33.5, 151.25, 15), MockCoordinates.parse(" -33,5 ", "151.25", "15"))
        assertEquals(MockCoordinates(-90.0, 180.0, 1000), MockCoordinates.parse("-90", "180", "1000"))
    }
    @Test fun rejectsNonfiniteOutOfRangeAndMalformedInput() {
        for (point in listOf(MockCoordinates(Double.NaN), MockCoordinates(longitude = Double.POSITIVE_INFINITY), MockCoordinates(91.0), MockCoordinates(longitude = -181.0), MockCoordinates(accuracy = 0), MockCoordinates(accuracy = 1001))) {
            assertThrows(IllegalArgumentException::class.java) { point.validate() }
        }
        assertThrows(IllegalArgumentException::class.java) { MockCoordinates.parse("bad", "0", "25") }
        assertThrows(IllegalArgumentException::class.java) { MockCoordinates.parse("0", "0", "2.5") }
    }
}
