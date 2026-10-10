# Keep the gomobile-bound classes: they are reached through JNI generated
# inside the combined AAR. Release R8 must preserve JNI class/member names.
-keep class io.nekohasekai.libbox.** { *; }
-keep class io.nekohasekai.mobilecore.** { *; }
-keep class io.nekohasekai.mobileexperiment.** { *; }
-keep class go.** { *; }
