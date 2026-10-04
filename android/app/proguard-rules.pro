# Keep the gomobile-bound classes: they are reached through JNI generated
# inside the AARs themselves, but keep names stable under any future minify.
-keep class io.nekohasekai.libbox.** { *; }
-keep class com.pidal.sakamoto.mobilecore.** { *; }
-keep class go.** { *; }
