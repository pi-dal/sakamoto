package com.pidal.sakamoto.runtime

/** An isolated map draft: editing never starts or changes a mock-location session. */
class LocationMapDraft(val isRoute: Boolean, initial: List<MockCoordinates>) {
    private val mutable = initial.map { it.validate() }.toMutableList()
    val points: List<MockCoordinates> get() = mutable.toList()
    init { require(initial.size <= if (isRoute) 32 else 1) }
    fun choose(point: MockCoordinates) {
        point.validate()
        if (isRoute) {
            require(mutable.size < 32) { "This route has 32 stops. Undo a stop before adding another." }
            mutable.lastOrNull()?.let { MockLocationRoute(listOf(it, point), 300).validate() }
            mutable.add(point)
        } else { mutable.clear(); mutable.add(point) }
    }
    fun undo() { if (mutable.isNotEmpty()) mutable.removeAt(mutable.lastIndex) }
    fun clear() { mutable.clear() }
    fun commit(): List<MockCoordinates> {
        if (isRoute) MockLocationRoute(points, 300).validate() else require(mutable.size == 1) { "Choose a location first" }
        return points
    }
}
