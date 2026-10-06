package com.pidal.sakamoto.runtime

/** Pure widget projection. Node latency is not an end-to-end VPN health claim. */
object WidgetPresentation {
    enum class Health { OFF, BUSY, UNCONFIRMED, NO_NETWORK, CHECKING, RUNNING, VERIFIED, UNVERIFIED }
    enum class Latency { NONE, UNTESTED, TESTING, FRESH, STALE, FAILED }
    data class Model(val health: Health, val node: String, val delay: Int, val latency: Latency, val testedAt: Long, val mode: String, val protocol: String = "", val group: String = "")

    fun resolve(state: RuntimeState, vpn: Boolean, physical: Boolean, groups: List<ConfigRepository.Group>, nowSeconds: Long): Model {
        val health = when {
            state.serviceState in setOf("Starting", "Stopping") -> Health.BUSY
            state.serviceState != "Running" -> if (vpn) Health.UNCONFIRMED else Health.OFF
            !vpn -> Health.UNCONFIRMED
            !physical -> Health.NO_NETWORK
            state.probeState == "Checking" -> Health.CHECKING
            state.probeState == "Reachable" -> Health.VERIFIED
            state.probeState == "Unverified" -> Health.UNVERIFIED
            else -> Health.RUNNING
        }
        val connected = state.serviceState == "Running" && vpn
        val primary = groups.firstOrNull { it.selectable }
        var tag = primary?.selected?.takeIf { it.isNotEmpty() } ?: state.selectedNode
        val seen = mutableSetOf<String>()
        var observation: ConfigRepository.GroupItem? = null
        while (tag.isNotEmpty() && seen.add(tag)) {
            val group = groups.firstOrNull { it.tag == tag }
            if (group == null) {
                observation = groups.asSequence().flatMap { it.items.asSequence() }.firstOrNull { it.tag == tag }
                break
            }
            // Never infer core selection from the fastest cached item.
            val selected = group.selected.takeIf { it.isNotEmpty() }
            if (selected == null) break
            tag = selected
        }
        val delay = observation?.delay ?: 0
        val testedAt = observation?.testedAt ?: 0
        val latency = when {
            !connected -> Latency.NONE
            state.selectedNodeTesting -> Latency.TESTING
            observation == null || testedAt <= 0 -> Latency.UNTESTED
            delay <= 0 -> Latency.FAILED
            nowSeconds - testedAt !in 0..120 -> Latency.STALE
            else -> Latency.FRESH
        }
        return Model(health, if (connected) tag else "", delay, latency, testedAt, state.routingMode,
            if (connected) observation?.type.orEmpty() else "", if (connected) primary?.tag.orEmpty() else "")
    }
}
