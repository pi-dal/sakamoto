package com.pidal.sakamoto

import com.pidal.sakamoto.ui.PolicyMatchType
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class PolicyMatchTypeTest {
    @Test fun selectedScopeUsesCanonicalPortablePolicySyntax() {
        assertEquals("suffix:aihubmix.com", PolicyMatchType.SUFFIX.match("*.aihubmix.com", "domain_suffix", "aihubmix.com"))
        assertEquals("suffix:aihubmix.com", PolicyMatchType.SUFFIX.match("https://aihubmix.com/chat", "domain", "aihubmix.com"))
        assertEquals("domain:aihubmix.com", PolicyMatchType.DOMAIN.match("aihubmix.com", "domain", "aihubmix.com"))
        assertEquals("keyword:aihub", PolicyMatchType.KEYWORD.inputForValidation("aihub"))
        assertEquals("*.aihubmix.com", PolicyMatchType.AUTOMATIC.match(" *.aihubmix.com ", "domain_suffix", "aihubmix.com"))
        assertThrows(IllegalArgumentException::class.java) { PolicyMatchType.SUFFIX.match("192.0.2.0/24", "ip_cidr", "192.0.2.0/24") }
        assertThrows(IllegalArgumentException::class.java) { PolicyMatchType.CIDR.match("aihubmix.com", "domain", "aihubmix.com") }
    }
}
