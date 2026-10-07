package com.pidal.sakamoto.ui

/**
 * Dedupes automatic clipboard-import offers across lifecycle transitions.
 * Pure state, no Android dependencies: the fragment feeds it content
 * identities (SHA-256 hashes) — never raw clipboard text.
 *
 * Semantics:
 *  - A payload identical to the last probed one is never offered twice;
 *    returning to RESUMED does not nag.
 *  - Every payload the user consumed or dismissed (including swipe-away and
 *    timeout) stays suppressed — alternating copies do not re-nag.
 *  - Genuinely new clipboard content always offers again.
 */
class ClipboardImportGate {
    enum class Decision { OFFER, SKIP_SHOWING, SKIP_HANDLED, SKIP_UNCHANGED }

    /** Identity of the payload probed most recently (whether offered or not). */
    var lastIdentity: String? = null
        private set

    /** Payloads already resolved by the user, bounded so memory stays small. */
    private val handled = object : LinkedHashMap<String, Unit>() {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, Unit>?) = size > 32
    }
    val handledIdentities: Set<String> get() = handled.keys

    private var showing = false

    fun decide(identity: String): Decision = when {
        handled.containsKey(identity) -> Decision.SKIP_HANDLED
        showing -> Decision.SKIP_SHOWING
        identity == lastIdentity -> Decision.SKIP_UNCHANGED
        else -> { lastIdentity = identity; Decision.OFFER }
    }

    fun offerShown() { showing = true }

    /** The user acted on the offer (import, swipe-away, timeout): suppress this payload. */
    fun userResolved(identity: String) {
        showing = false
        handled[identity] = Unit
    }

    /** The offer left the screen without a user decision (tab switch, app pause). */
    fun navigationResolved() { showing = false }

    /** Restores dedup state across configuration changes. */
    fun restore(handledIdentities: Collection<String>, last: String?) {
        handled.clear()
        handledIdentities.forEach { handled[it] = Unit }
        lastIdentity = last
    }
}
