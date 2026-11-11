package com.pidal.sakamoto

import android.app.Instrumentation
import android.content.Context
import android.content.ContextWrapper
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.ProxyChainSettings
import com.pidal.sakamoto.runtime.RoutingSnapshot
import io.nekohasekai.libbox.Libbox
import org.json.JSONObject
import java.io.File

/** Isolated profile storage and actual JNI validation, no VPN/network takeover. */
object ProxyChainScenario {
    fun run(instrumentation: Instrumentation) {
        val owner = instrumentation.targetContext
        val temporary = File(owner.cacheDir, "chain-scenario").apply { mkdirs() }
        val context = object : ContextWrapper(owner) {
            override fun getFilesDir(): File = temporary
        }
        val uuid = "00000000-0000-4000-8000-000000000001"
        val base = """{"log":{"level":"error"},"inbounds":[],"outbounds":[{"type":"selector","tag":"MainProxy","outbounds":["Entries","ExitOnly"],"default":"ExitOnly"},{"type":"urltest","tag":"Entries","outbounds":["Entry","Landing"]},{"type":"selector","tag":"ExitOnly","outbounds":["Landing"]},{"type":"vless","tag":"Entry","server":"127.0.0.1","server_port":18001,"uuid":"$uuid"},{"type":"vless","tag":"Landing","server":"127.0.0.1","server_port":18002,"uuid":"$uuid"},{"type":"vmess","tag":"Other","server":"127.0.0.1","server_port":18003,"uuid":"$uuid","security":"auto"},{"type":"direct","tag":"direct"}],"route":{"final":"MainProxy","rules":[{"clash_mode":"Global","action":"route","outbound":"MainProxy"},{"type":"logical","mode":"or","rules":[{"domain":["example.com"]}],"outbound":"MainProxy","action":"route"},{"domain":["direct.example"],"outbound":"direct","action":"route"}]},"dns":{"servers":[{"type":"https","tag":"remote","server":"dns.example","detour":"MainProxy"}]}}"""
        try {
            Libbox.checkConfig(base)
            ConfigRepository.save(context, ConfigRepository.StagedConfig(generatedContent = base))
            val initial = ConfigRepository.load(context)
            val choice = ProxyChainSettings(enabled = true, exit = "Landing")
            ConfigRepository.saveProxyChain(context, choice, initial)
            val saved = ConfigRepository.load(context)
            check(saved.proxyChain == choice && saved.generatedContent == base)
            val runtime = ConfigRepository.readGeneratedContent(context)!!
            Libbox.checkConfig(runtime)
            val parsed = JSONObject(runtime)
            val nodes = parsed.getJSONArray("outbounds")
            for (i in 0 until nodes.length()) {
                val row = nodes.getJSONObject(i)
                check(row.optString("tag") != "ExitOnly")
                row.optJSONArray("outbounds")?.let { members -> check((0 until members.length()).none { members.getString(it) == "Landing" }) }
                if (row.optString("tag") == "MainProxy") check(row.optString("default") == "Entries")
            }
            check(parsed.getJSONObject("route").getString("final") == "Landing")
            check(parsed.getJSONObject("dns").getJSONArray("servers").getJSONObject(0).getString("detour") == "Landing")
            check(RoutingSnapshot.parse(runtime).exits.any { it.tag == "Landing" && it.type == "vless" })
            check(ProxyChainSettings.imported(runtime) == choice)
            ConfigRepository.saveProxyChain(context, choice.copy(exit = "Other"), saved)
            check(ConfigRepository.load(context).runtimeContent.contains("\"final\":\"Other\""))
            Libbox.checkConfig(ConfigRepository.load(context).runtimeContent)
            var rejected = 0
            for (bad in listOf(choice.copy(exit = "Missing"), choice.copy(upstream = "Landing"), choice.copy(upstream = "ExitOnly"))) {
                val before = ConfigRepository.load(context)
                try { ConfigRepository.saveProxyChain(context, bad, before) } catch (_: Exception) { rejected++ }
                check(ConfigRepository.load(context) == before)
            }
            check(rejected == 3)
            val current = ConfigRepository.load(context)
            ConfigRepository.saveProxyChain(context, choice.copy(enabled = false), current)
            check(ConfigRepository.readGeneratedContent(context) == base)
            val receipt = JSONObject().put("passed", true).put("platform", "android").put("native_version", Libbox.version())
                .put("vless_exit", true).put("vmess_exit", true).put("profile_roundtrip", true).put("empty_group_pruned", true)
                .put("default_repaired", true).put("dns_uses_exit", true).put("disable_preserves_base", true)
                .put("invalid_choices_rejected", rejected).put("native_validation", true)
            File(owner.cacheDir, "android-chain.json").writeText(receipt.toString(2))
        } finally { temporary.deleteRecursively() }
    }
}
