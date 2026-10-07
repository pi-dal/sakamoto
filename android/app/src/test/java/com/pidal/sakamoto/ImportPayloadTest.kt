package com.pidal.sakamoto

import com.pidal.sakamoto.ui.ImportPayload
import org.junit.Assert.*
import org.junit.Test

class ImportPayloadTest {
    @Test fun routesSupportedShapesWithoutReadingCredentials() {
        assertEquals(ImportPayload.Kind.NODE, ImportPayload.detect("  vless://id@example.org:443#node ").kind)
        assertEquals(ImportPayload.Kind.URL, ImportPayload.detect("https://example.org/sub?token=secret").kind)
        assertEquals(ImportPayload.Kind.CONFIG_JSON, ImportPayload.detect("\uFEFF{\"outbounds\":[]}").kind)
        assertEquals(ImportPayload.Kind.CONF, ImportPayload.detect("[Rule]\nFINAL,proxy").kind)
    }
    @Test fun rejectsUnrelatedAmbiguousAndOversizedInput() {
        for (raw in listOf("", "ordinary text", "file:///etc/config", "https://", "vless://one\nvless://two", "a".repeat(1_048_577))) {
            assertThrows(Exception::class.java) { ImportPayload.detect(raw) }
        }
    }
    @Test fun qrDecoderPreservesPayloadThenUsesTheSameRouter() {
        val raw = "vless://11111111-1111-4111-8111-111111111111@example.org:443#Test"
        val matrix = com.google.zxing.qrcode.QRCodeWriter().encode(raw, com.google.zxing.BarcodeFormat.QR_CODE, 256, 256)
        val pixels = IntArray(256 * 256) { index -> if (matrix[index % 256, index / 256]) -0x1000000 else -1 }
        val source = com.google.zxing.RGBLuminanceSource(256, 256, pixels)
        val bitmap = com.google.zxing.BinaryBitmap(com.google.zxing.common.HybridBinarizer(source))
        val decoded = com.google.zxing.qrcode.QRCodeReader().decode(bitmap).text
        assertEquals(raw, decoded)
        assertEquals(ImportPayload.Kind.NODE, ImportPayload.detect(decoded).kind)
    }

    @Test fun preservesTheOriginalLinkForTheExistingParser() {
        val raw = "ss://secret@example.org:443#My%20node"
        assertEquals(raw, ImportPayload.detect(raw).text)
    }
}
