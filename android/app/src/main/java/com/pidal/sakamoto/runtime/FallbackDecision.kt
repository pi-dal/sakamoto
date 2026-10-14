package com.pidal.sakamoto.runtime

/** A round uses only tests newer than its start and preserves manual selection. */
object FallbackDecision {
    fun selectedObservation(groups: List<ConfigRepository.Group>): ConfigRepository.GroupItem? {
        var tag = groups.firstOrNull { it.tag == "MainProxy" }?.selected ?: return null
        val seen = mutableSetOf<String>()
        while (tag.isNotEmpty() && seen.add(tag)) {
            val group = groups.firstOrNull { it.tag == tag }
            if (group == null) return groups.asSequence().flatMap { it.items.asSequence() }.firstOrNull { it.tag == tag }
            tag = group.selected
        }
        return null
    }

    fun resultWaitMillis(tags: Collection<String>, groups: List<ConfigRepository.Group>): Long {
        fun leaves(tag: String, visiting: Set<String>): Set<String> {
            val group = groups.firstOrNull { it.tag == tag }
            if (tag in visiting || group == null || group.items.isEmpty()) return setOf(tag)
            return group.items.flatMap { leaves(it.tag, visiting + tag) }.toSet()
        }
        val members = tags.maxOfOrNull { leaves(it, emptySet()).size } ?: 1
        return (5L + ((members + 9) / 10).coerceAtLeast(1) * 15L) * 1000L
    }

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
