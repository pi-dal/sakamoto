package com.pidal.sakamoto.runtime

import io.nekohasekai.libbox.CommandServer
import io.nekohasekai.mobileexperiment.LearningProber

/** Capture the instance once, so late verification cannot use a replacement core. */
class NativeLearningProber(private val server: CommandServer, private val instanceID: Long) : LearningProber {
    override fun probeOrigin(domain: String, port: Int, outbound: String, timeoutMillis: Int): Int =
        server.probeAutomaticOrigin(domain, port, outbound, instanceID, timeoutMillis)
}
