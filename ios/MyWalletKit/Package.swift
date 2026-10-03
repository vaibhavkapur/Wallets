// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "MyWalletKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [
        .library(name: "MyWalletKit", targets: ["MyWalletKit"]),
    ],
    targets: [
        .target(name: "MyWalletKit"),
        .testTarget(name: "MyWalletKitTests", dependencies: ["MyWalletKit"]),
    ]
)
