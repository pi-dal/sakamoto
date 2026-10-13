package com.pidal.sakamoto.ui

import com.pidal.sakamoto.R

/** Visible match choices produce the existing portable host policy syntax. */
enum class PolicyMatchType(val label: Int, val help: Int) {
    AUTOMATIC(R.string.policy_match_automatic, R.string.policy_match_automatic_help),
    DOMAIN(R.string.ux_editing_pred_domain, R.string.policy_match_exact_help),
    SUFFIX(R.string.ux_editing_pred_domain_suffix, R.string.policy_match_suffix_help),
    KEYWORD(R.string.ux_editing_pred_domain_keyword, R.string.policy_match_keyword_help),
    CIDR(R.string.ux_editing_pred_ip_cidr, R.string.policy_match_cidr_help);

    fun inputForValidation(raw: String): String {
        val text = raw.trim()
        return if (this == KEYWORD && !text.startsWith("keyword:", true)) "keyword:$text" else text
    }

    fun match(raw: String, kind: String, value: String): String = when (this) {
        AUTOMATIC -> raw.trim()
        DOMAIN, SUFFIX -> {
            require(kind in setOf("domain", "domain_suffix") && value.isNotEmpty() && value.none { it.isWhitespace() || it in "/*:" }) {
                "Enter a domain or HTTP(S) URL, or choose a different match type"
            }
            (if (this == SUFFIX) "suffix:" else "domain:") + value
        }
        KEYWORD -> "keyword:$value"
        CIDR -> { require(kind == "ip_cidr") { "Enter an IP address or CIDR range" }; "cidr:$value" }
    }
}
