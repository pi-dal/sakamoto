package com.pidal.sakamoto

import android.app.Instrumentation
import android.content.Intent
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.TunnelPackage
import com.pidal.sakamoto.ui.ConfigFragment
import okhttp3.OkHttpClient
import org.json.JSONObject
import java.io.File
import java.net.InetAddress
import java.security.KeyStore
import java.security.cert.CertificateFactory
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509TrustManager

object SourceDownloadScenario {
    fun run(test: Instrumentation, leavePending: Boolean = false) {
        val ctx = test.targetContext
        val cert = File(ctx.cacheDir, "source.pem").inputStream().use { CertificateFactory.getInstance("X.509").generateCertificate(it) }
        val keys = KeyStore.getInstance(KeyStore.getDefaultType()).apply { load(null); setCertificateEntry("fixture", cert) }
        val manager = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm()).apply { init(keys) }.trustManagers.filterIsInstance<X509TrustManager>().single()
        val tls = SSLContext.getInstance("TLS").apply { init(null, arrayOf(manager), null) }
        val lookups = java.util.concurrent.CopyOnWriteArrayList<String>()
        val received = CountDownLatch(1)
        val release = CountDownLatch(1)
        val client = OkHttpClient.Builder().proxy(java.net.Proxy.NO_PROXY).followRedirects(false).sslSocketFactory(tls.socketFactory, manager)
            .dns(object : okhttp3.Dns {
                override fun lookup(hostname: String): List<InetAddress> {
                    lookups.add(hostname)
                    return listOf(InetAddress.getLoopbackAddress())
                }
            })
            .addNetworkInterceptor { chain ->
                val response = chain.proceed(chain.request())
                if (chain.request().url.encodedPath == "/delay.conf") { received.countDown(); check(release.await(10, TimeUnit.SECONDS)) }
                response
            }.build()
        val screen = ConfigFragment(client)
        val activity = test.startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        fun download(url: String) = test.runOnMainSync { screen.importFromUrl(url, ConfigRepository.load(ctx)) }
        fun waitForImport() {
            val end = System.nanoTime() + 10_000_000_000L
            while (System.nanoTime() < end) {
                var done = false
                test.runOnMainSync { done = !screen.importing }
                if (done) return
                Thread.sleep(20)
            }
            error("URL import did not finish")
        }
        val a = ConfigRepository.load(ctx)
        try {
            test.runOnMainSync { activity.openChild(screen, ctx.getString(R.string.config_import_title)); activity.supportFragmentManager.executePendingTransactions() }
            if (leavePending) {
                download("https://allowed.fixture.example:18585/ok.conf"); waitForImport()
                return
            }
            val protected = a.copy(regionalDNS = mapOf("domains" to "protected.fixture.example"))
            ConfigRepository.save(ctx, protected)
            download("https://Protected.Fixture.Example.:18585/deny.conf"); waitForImport()
            check(lookups.isEmpty() && ConfigRepository.load(ctx) == protected)
            download("https://allowed.fixture.example:18585/ok.conf"); waitForImport()
            check(lookups.isNotEmpty() && ConfigRepository.load(ctx).sourceConfContent.contains("downloaded fixture"))
            ConfigRepository.save(ctx, a)
            download("https://allowed.fixture.example:18585/delay.conf")
            check(received.await(10, TimeUnit.SECONDS))
            File(ctx.cacheDir, "B.zip").inputStream().use { TunnelPackage.import(ctx, it) }
            val b = ConfigRepository.load(ctx)
            release.countDown(); waitForImport()
            check(a.modeCacheID != b.modeCacheID && ConfigRepository.load(ctx) == b && !b.sourceNeedsGenerate)
            File(ctx.cacheDir, "source-download.json").writeText(JSONObject().put("protected_resolver_calls", 0).put("allowed_resolver_calls", lookups.size).put("allowed_download", true).put("stale_response_preserved_B", true).toString(2))
        } finally { release.countDown(); test.runOnMainSync { activity.finish() } }
    }
}
