package com.pidal.sakamoto.runtime

/** A round uses only tests newer than its start and preserves manual selection. */
object FallbackDecision {
    fun candidate(groups: List<ConfigRepository.Group>, selector: String, chain: List<String>, started: Long): String? {
        val selected = groups.firstOrNull { it.tag == selector }?.selected ?: return null
        if (selected !in chain) return null
        return chain.firstOrNull { tag ->
            val group = groups.firstOrNull { it.tag == tag }
            val items = group?.items ?: groups.flatMap { it.items }.filter { it.tag == tag }
            items.any { it.delay > 0 && it.testedAt >= started }
        }
    }
}
