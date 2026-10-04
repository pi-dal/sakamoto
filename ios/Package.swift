// swift-tools-version: 5.10
import PackageDescription

// sakamoto iOS integration skeleton.
//
// Scope (see README.md): the real, compilable contract between an iOS app,
// its PacketTunnelProvider, and the pkg/mobilecore vocabulary. This package
// deliberately contains NO Libbox import: Libbox.xcframework is not in the
// repository yet, and targets that need it must fail loudly instead of
// faking success. The tunnel-provider source that imports Libbox lives in
// Extension/ and is compiled only by the future Xcode app project.
let package = Package(
    name: "sakamoto-ios",
    platforms: [
        // Keep in sync with build-libbox.sh (-iosversion=15.0, libbox parity).
        .iOS(.v15),
        .macOS(.v13),
    ],
    products: [
        .library(name: "SakamotoKit", targets: ["SakamotoKit"]),
        .library(name: "SakamotoNE", targets: ["SakamotoNE"]),
    ],
    targets: [
        // Vocabulary, wire models and protocol boundaries. Foundation only.
        .target(name: "SakamotoKit"),
        // NetworkExtension-backed TunnelControlling implementation. Compiles
        // against the macOS/iOS SDK without signing; runtime behavior needs
        // an entitlement-bearing app target (not created yet).
        .target(name: "SakamotoNE", dependencies: ["SakamotoKit"]),
        .testTarget(name: "SakamotoKitTests", dependencies: [
            "SakamotoKit",
            "SakamotoNE",
        ]),
    ]
)
