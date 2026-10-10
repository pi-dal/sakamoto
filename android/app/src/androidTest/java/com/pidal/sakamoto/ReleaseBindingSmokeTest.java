package com.pidal.sakamoto;

import android.app.Activity;
import android.app.Instrumentation;
import android.appwidget.AppWidgetManager;
import android.appwidget.AppWidgetProviderInfo;
import android.content.Intent;
import android.content.pm.ResolveInfo;
import android.net.VpnService;
import android.os.Bundle;
import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;
import android.widget.RemoteViews;
import android.widget.TextView;

import io.nekohasekai.libbox.Libbox;
import io.nekohasekai.mobilecore.Mobilecore;
import io.nekohasekai.mobileexperiment.ExperimentTracker;
import io.nekohasekai.mobileexperiment.Mobileexperiment;

/** Exercise the actual R8 APK using platform APIs and explicitly kept JNI.
 * Kotlin helpers and app implementation methods may be removed by R8 when
 * they are used only by instrumentation, so this runner never calls them.
 * This test does not request VPN consent or start a tunnel.
 */
public class ReleaseBindingSmokeTest extends Instrumentation {
    private boolean navigation;

    @Override public void onCreate(Bundle arguments) {
        super.onCreate(arguments);
        navigation = arguments != null && "true".equals(arguments.getString("navigation"));
        start();
    }

    private static void require(boolean value, String message) {
        if (!value) throw new AssertionError(message);
    }

    private static boolean containsText(View view, String text) {
        if (view instanceof TextView && text.contentEquals(((TextView) view).getText())) return true;
        if (view instanceof ViewGroup) {
            ViewGroup group = (ViewGroup) view;
            for (int i = 0; i < group.getChildCount(); i++) {
                if (containsText(group.getChildAt(i), text)) return true;
            }
        }
        return false;
    }

    @android.annotation.SuppressLint("DiscouragedApi")
    private int resource(String name, String type) {
        // R classes are implementation details and can disappear from the app.
        int id = getTargetContext().getResources().getIdentifier(name, type, getTargetContext().getPackageName());
        require(id != 0, "Resource missing: " + type + "/" + name);
        return id;
    }

    private void checkMain(Runnable assertion) throws Throwable {
        java.util.concurrent.atomic.AtomicReference<Throwable> failure = new java.util.concurrent.atomic.AtomicReference<>();
        runOnMainSync(() -> {
            try { assertion.run(); } catch (Throwable error) { failure.set(error); }
        });
        if (failure.get() != null) throw failure.get();
    }

    private void checkNavigation() throws Throwable {
        Intent intent = new Intent(Intent.ACTION_MAIN)
                .setClassName(getTargetContext().getPackageName(), "com.pidal.sakamoto.MainActivity")
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        Activity activity = startActivitySync(intent);
        waitForIdleSync();
        int data = resource("nav_data", "id");
        int settings = resource("nav_settings", "id");
        int[] pages = {resource("nav_config", "id"), data, settings, resource("nav_home", "id")};
        for (int page : pages) {
            checkMain(() -> {
                View tab = activity.findViewById(page);
                require(tab != null && tab.performClick(), "Navigation tab missing");
            });
            waitForIdleSync();
            checkMain(() -> {
                View content = activity.findViewById(resource("fragment_container", "id"));
                require(content != null && content.getVisibility() == View.VISIBLE, "Page content missing");
                View tabs = activity.findViewById(resource("bottom_bar", "id"));
                require(tabs != null && tabs.getVisibility() == View.VISIBLE, "Tab bar missing");
                if (page == data) {
                    require(containsText(content, getTargetContext().getString(resource("data_uplink", "string"))), "Data page did not render");
                }
                if (page == settings) {
                    require(containsText(content, getTargetContext().getString(resource("settings_tailscale_entry", "string"))), "Settings page did not render");
                }
            });
        }
        runOnMainSync(activity::finish);
        waitForIdleSync();
    }

    @Override public void onStart() {
        Bundle results = new Bundle();
        try {
            // Application.onCreate initializes libbox on the main thread.
            runOnMainSync(() -> {});
            require("Reachable".equals(Mobilecore.sessionPhase("Running", "Reachable", false)), "Mobilecore JNI failed");
            require("Global".equals(Mobilecore.nextRoutingMode("Rule")), "Routing JNI failed");
            require(!Libbox.version().trim().isEmpty(), "Libbox JNI failed");
            Mobilecore.validateConfigJSON("{\"log\":{},\"dns\":{},\"inbounds\":[{\"type\":\"tun\"}],\"outbounds\":[{\"type\":\"direct\",\"tag\":\"direct\"}],\"route\":{\"final\":\"direct\"}}");
            ExperimentTracker tracker = Mobileexperiment.newExperimentTracker(1);
            tracker.connection("test", "example.com", "93.184.215.14:443", "tcp", "direct", "", false, 0, 1000000);
            require("example.com".equals(tracker.observeLog("connection: open connection to example.com:443 using outbound/direct[direct]: dial tcp 93.184.215.14:443: i/o timeout", 1000000, true, true, false)), "Experiment JNI failed");
            boolean vpn = false;
            Intent service = new Intent(VpnService.SERVICE_INTERFACE).setPackage(getTargetContext().getPackageName());
            for (ResolveInfo info : getTargetContext().getPackageManager().queryIntentServices(service, 0)) {
                vpn |= "android.permission.BIND_VPN_SERVICE".equals(info.serviceInfo.permission);
            }
            require(vpn, "VPN service registration missing");
            int providers = 0;
            for (AppWidgetProviderInfo provider : AppWidgetManager.getInstance(getTargetContext()).getInstalledProviders()) {
                if (getTargetContext().getPackageName().equals(provider.provider.getPackageName())) providers++;
            }
            require(providers == 3, "Widget provider registration missing");
            checkMain(() -> {
                for (String name : new String[]{"widget_vpn", "widget_vpn_toggle", "widget_vpn_compact"}) {
                    new RemoteViews(getTargetContext().getPackageName(), resource(name, "layout"))
                            .apply(getTargetContext(), new FrameLayout(getTargetContext()));
                }
            });
            if (navigation) checkNavigation();
            results.putString("stream", "PASS: R8 release JNI (libbox/mobilecore/mobileexperiment), VPN registration, three widget providers/layouts" + (navigation ? ", Config/Data/Settings/Home navigation" : "") + "\n");
            finish(Activity.RESULT_OK, results);
        } catch (Throwable error) {
            String message = error.getMessage() == null ? "" : error.getMessage();
            message = message.replaceAll("https?://\\S+", "[URL]");
            results.putString("stream", "FAIL: R8 release: " + error.getClass().getSimpleName() + ": " + message.substring(0, Math.min(200, message.length())) + "\n");
            finish(Activity.RESULT_CANCELED, results);
        }
    }
}
