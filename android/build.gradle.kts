// sakamoto Android — root build script.
//
// Version choices (all stable, mutually compatible, JDK 17 toolchain):
//   Gradle 8.9        — pinned by gradle/wrapper/gradle-wrapper.properties
//   AGP 8.7.3         — works with Gradle 8.9 and compileSdk 35
//   Kotlin 2.0.21     — stable kotlin-android plugin, no Compose compiler
//
// The upstream SagerNet/sing-box-for-android currently builds with much newer
// AGP/Kotlin on its dev branch; we pin a conservative set because this
// machine has no Android SDK/JDK yet and every chosen combination here is a
// well-known stable pairing (documented in android/README.md).

plugins {
    id("com.android.application") version "8.7.3" apply false
    id("org.jetbrains.kotlin.android") version "2.0.21" apply false
}
