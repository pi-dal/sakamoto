plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

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
        versionCode = 1
        versionName = "0.1.0"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
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
    }
}

dependencies {
    // Local gomobile bindings — built by android/scripts/*.sh, never committed.
    // The default API-24 build uses the full libbox AAR. The legacy API-21
    // AAR is generated for a future flavor split and must not be linked into
    // the same variant (both AARs export identical Java package names).
    implementation(files("libs/libbox.aar", "libs/mobilecore.aar"))

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
