package com.pidal.sakamoto.runtime

/** Background health checks back off only after successful fresh rounds. */
class ExperimentProbeSchedule {
    private var healthyRounds = 0
    @Synchronized fun record(healthy: Boolean) { healthyRounds = if (healthy) (healthyRounds + 1).coerceAtMost(3) else 0 }
    @Synchronized fun reset() { healthyRounds = 0 }
    @Synchronized fun delayMillis(learning: Boolean): Long {
        val seconds = listOf(30L, 60L, 120L, 300L)[healthyRounds]
        return (if (learning) seconds.coerceAtMost(60L) else seconds) * 1000L
    }
}
