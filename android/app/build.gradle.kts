plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// Release credentials are provided by the private local signing directory or
// CI secrets. Never fall back to a debug certificate for a public APK.
val releaseStore = providers.environmentVariable("SAKAMOTO_ANDROID_KEYSTORE").orNull

android {
    namespace = "com.pidal.sakamoto"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.pidal.sakamoto"
        // sing-box-for-android declares minSdk 21; the 21–23 floor is served
        // by its libbox-legacy.aar variant (built with -androidapi 21 and no
        // with_naive_outbound). This project wires only the MAIN libbox
        // variant (-androidapi 24), so the declared floor is 24. The legacy
        // AAR is still produced by scripts/build-libbox.sh for a future
        // flavor split — see android/README.md "API levels".
        minSdk = 24
        targetSdk = 35
        versionCode = 2
        versionName = "0.1.0"
        testInstrumentationRunner = "com.pidal.sakamoto.NativeBindingSmokeTest"
    }

    signingConfigs {
        if (releaseStore != null) create("official") {
            storeFile = file(releaseStore)
            storePassword = providers.environmentVariable("SAKAMOTO_ANDROID_STORE_PASSWORD").get()
            keyAlias = providers.environmentVariable("SAKAMOTO_ANDROID_KEY_ALIAS").get()
            keyPassword = providers.environmentVariable("SAKAMOTO_ANDROID_KEY_PASSWORD").get()
        }
    }

    buildTypes {
        release {
            if (releaseStore != null) signingConfig = signingConfigs.getByName("official")
            isMinifyEnabled = false
            isDebuggable = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    buildFeatures {
        viewBinding = true
        buildConfig = true
    }
}

dependencies {
    // Local gomobile bindings — built by android/scripts/*.sh, never committed.
    // libbox and mobilecore are bound together so there is exactly one
    // gomobile JNI runtime and one go.Seq class in the process.
    implementation(files("libs/libbox.aar"))

    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("com.google.android.material:material:1.12.0")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.7")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")

    // JVM unit tests (run with ./gradlew test once a JDK/SDK exists).
    // org.json ships as android.jar stubs; the real artifact makes the pure
    // JSON injection model testable on the JVM classpath.
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20240303")
}
