import XCTest
@testable import SakamotoKit

/// Foreground clipboard offers dedupe by UIPasteboard.changeCount revision —
/// presence checks only, the payload is never read silently.
final class ClipboardOfferTests: XCTestCase {
    func testOffersOncePerRevisionAndDismissalSuppresses() {
        var offer = ClipboardOffer()
        XCTAssertTrue(offer.shouldOffer(revision: 10))
        // Repeated foreground probes against the same revision still offer
        // while the row is up, but dismissal suppresses every later probe.
        offer.dismiss(revision: 10)
        XCTAssertFalse(offer.shouldOffer(revision: 10))
        XCTAssertFalse(offer.shouldOffer(revision: 10))
    }

    func testNewClipboardRevisionOffersAgain() {
        var offer = ClipboardOffer()
        offer.dismiss(revision: 10)
        XCTAssertTrue(offer.shouldOffer(revision: 11))
        offer.dismiss(revision: 11)
        XCTAssertFalse(offer.shouldOffer(revision: 11))
        XCTAssertTrue(offer.shouldOffer(revision: 12))
    }

    func testPasteConsumptionResolvesTheOffer() {
        var offer = ClipboardOffer()
        XCTAssertTrue(offer.shouldOffer(revision: 5))
        offer.dismiss(revision: 5) // PasteButton delivery consumes the offer too
        XCTAssertFalse(offer.shouldOffer(revision: 5))
    }

    func testOfferStateDoesNotLeakAcrossUnrelatedRevisions() {
        var offer = ClipboardOffer()
        offer.dismiss(revision: 1)
        for revision in 2...20 where revision != 7 {
            XCTAssertTrue(offer.shouldOffer(revision: revision))
        }
        XCTAssertTrue(offer.shouldOffer(revision: 7))
        XCTAssertFalse(offer.shouldOffer(revision: 1))
    }

    func testHonestContentWordingHasNoProtocolClaim() {
        // The offer describes presence (text/link), never a parsed protocol —
        // the app cannot claim "node" or "subscription" without reading.
        XCTAssertEqual(ClipboardOffer.Content.text, ClipboardOffer.Content.text)
        XCTAssertNotEqual(ClipboardOffer.Content.text, ClipboardOffer.Content.link)
    }
}
