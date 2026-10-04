// sakamoto Android — Gradle settings.
//
// Repository layout mirrors the plugin/dependency reality of this machine's
// tooling policy: google() for AndroidX/AGP, mavenCentral() for Kotlin and
// coroutines. The libbox.aar / mobilecore.aar bindings are NOT Maven
// artifacts; they are built locally by android/scripts/*.sh into app/libs/
// (git-ignored) and picked up by app/build.gradle.kts's fileTree.

pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "sakamoto"
include(":app")
