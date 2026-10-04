package com.pidal.sakamoto.runtime

import io.nekohasekai.libbox.TailscaleEndpointStatus
import io.nekohasekai.libbox.TailscaleEndpointStatusIterator
import io.nekohasekai.libbox.TailscalePeer
import io.nekohasekai.libbox.TailscalePingResult

/**
 * libbox binding glue: map gomobile-bound v1.14.2 status types into the pure
 * summaries in TailscaleModels. This file touches libbox classes directly,
 * so it can only compile against the real AAR — every other Tailscale file
 * is deliberately pure or libbox-free for unit testing.
 *
 * STATUS PENDING SDK BUILD VERIFICATION — android/README.md.
 */
object TailscaleBinding {

    fun mapUpdate(endpoints: TailscaleEndpointStatusIterator): List<TailscaleEndpointSummary> {
        val result = mutableListOf<TailscaleEndpointSummary>()
        while (endpoints.hasNext()) {
            result.add(mapEndpoint(endpoints.next()))
        }
        return result
    }

    fun mapEndpoint(status: TailscaleEndpointStatus): TailscaleEndpointSummary {
        val peers = mutableListOf<TailscalePeerSummary>()
        val groups = status.userGroups()
        while (groups.hasNext()) {
            val group = groups.next()
            val peerIterator = group.peers()
            while (peerIterator.hasNext()) {
                peers.add(mapPeer(peerIterator.next()))
            }
        }
        val self = status.self?.let(::mapPeer)
        val exit = status.exitNode?.let(::mapPeer)
        // The core reports peers via user groups; make sure the self/exit
        // facts are never missing from the list.
        self?.let { peer -> if (peers.none { it.stableID == peer.stableID }) peers.add(peer) }
        exit?.let { peer -> if (peers.none { it.stableID == peer.stableID }) peers.add(peer) }
        return TailscaleEndpointSummary(
            endpointTag = status.endpointTag,
            backendState = TailscaleBackendState.parse(status.backendState),
            networkName = status.networkName,
            magicDNSSuffix = status.magicDNSSuffix,
            authURL = status.authURL,
            selfPeer = self,
            exitNodePeer = exit,
            peers = peers,
        )
    }

    fun mapPeer(peer: TailscalePeer): TailscalePeerSummary {
        val ips = mutableListOf<String>()
        val ipIterator = peer.tailscaleIPs()
        while (ipIterator.hasNext()) ips.add(ipIterator.next())
        return TailscalePeerSummary(
            stableID = peer.stableID,
            hostName = peer.hostName,
            dnsName = peer.dnsName,
            os = peer.os,
            tailscaleIPs = ips,
            online = peer.online,
            active = peer.active,
            expired = peer.expired,
            exitNode = peer.exitNode,
            exitNodeOption = peer.exitNodeOption,
            lastSeenUnixSeconds = peer.lastSeen,
        )
    }

    fun mapPing(result: TailscalePingResult): TailscalePingOutcome = TailscalePingOutcome(
        latencyMs = result.latencyMs,
        direct = result.isDirect,
        endpoint = result.endpoint,
        peerRelay = result.peerRelay,
        derpRegionCode = result.derpRegionCode,
        error = result.error,
    )
}
