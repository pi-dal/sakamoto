package com.pidal.sakamoto

import com.pidal.sakamoto.ui.ClipboardImportGate
import org.junit.Assert.*
import org.junit.Test

class ClipboardImportGateTest {
    private fun h(seed: String) = "sha256-$seed"

    @Test fun offersOncePerPayloadAndDeduplicatesLaterProbes() {
        val gate = ClipboardImportGate()
        assertEquals(ClipboardImportGate.Decision.OFFER, gate.decide(h("a")))
        gate.offerShown()
        // Returning to the foreground while the offer is up does not re-offer.
        assertEquals(ClipboardImportGate.Decision.SKIP_SHOWING, gate.decide(h("a")))
        gate.userResolved(h("a"))
        // Many later probes against the same payload stay silent.
        assertEquals(ClipboardImportGate.Decision.SKIP_HANDLED, gate.decide(h("a")))
        assertEquals(ClipboardImportGate.Decision.SKIP_HANDLED, gate.decide(h("a")))
    }

    @Test fun newClipboardContentOffersAgain() {
        val gate = ClipboardImportGate()
        gate.decide(h("old")); gate.offerShown(); gate.userResolved(h("old"))
        assertEquals(ClipboardImportGate.Decision.OFFER, gate.decide(h("new")))
        gate.userResolved(h("new"))
        assertEquals(ClipboardImportGate.Decision.SKIP_HANDLED, gate.decide(h("old")))
        assertEquals(ClipboardImportGate.Decision.SKIP_HANDLED, gate.decide(h("new")))
    }

    @Test fun navigationWithoutDecisionDoesNotSuppressButDoesNotReoffer() {
        val gate = ClipboardImportGate()
        gate.decide(h("a")); gate.offerShown()
        gate.navigationResolved()
        // Leaving while offered does not mark the payload handled.
        assertTrue(gate.handledIdentities.isEmpty())
        // Re-entering with the clipboard unchanged does not nag.
        assertEquals(ClipboardImportGate.Decision.SKIP_UNCHANGED, gate.decide(h("a")))
        // If the user copies the same text again after other content, it offers.
        gate.decide(h("other"))
        assertEquals(ClipboardImportGate.Decision.OFFER, gate.decide(h("a")))
    }

    @Test fun nonImportableContentIsStillRecordedSoRechecksAreCheap() {
        val gate = ClipboardImportGate()
        assertEquals(ClipboardImportGate.Decision.OFFER, gate.decide(h("a")))
        gate.offerShown()
        gate.navigationResolved()
        // Unsupported text never reached decide upstream — but a supported one
        // copied afterwards is still a fresh offer.
        assertEquals(ClipboardImportGate.Decision.OFFER, gate.decide(h("b")))
    }

    @Test fun restoreSurvivesFragmentRecreation() {
        val first = ClipboardImportGate()
        first.decide(h("a")); first.offerShown(); first.userResolved(h("a"))
        val recreated = ClipboardImportGate()
        recreated.restore(first.handledIdentities, first.lastIdentity)
        assertEquals(ClipboardImportGate.Decision.SKIP_HANDLED, recreated.decide(h("a")))
        assertEquals(ClipboardImportGate.Decision.OFFER, recreated.decide(h("b")))
    }

    @Test fun identicalRecopyAfterDismissalStaysSilent() {
        // Swipe-away counts as dismissal; re-copying byte-identical content
        // must not re-nag (handled wins over unchanged).
        val gate = ClipboardImportGate()
        gate.decide(h("a")); gate.offerShown(); gate.userResolved(h("a"))
        gate.decide(h("b"))
        assertEquals(ClipboardImportGate.Decision.SKIP_HANDLED, gate.decide(h("a")))
    }
}
