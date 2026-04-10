// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "SecondBrain",
    platforms: [
        .macOS(.v14)
    ],
    products: [
        .executable(name: "SecondBrain", targets: ["SecondBrain"]),
    ],
    dependencies: [
        .package(url: "https://github.com/groue/GRDB.swift.git", from: "7.5.0"),
        .package(url: "https://github.com/jpsim/Yams.git", from: "5.1.0"),
        .package(url: "https://github.com/apple/swift-markdown.git", from: "0.5.0"),
    ],
    targets: [
        .executableTarget(
            name: "SecondBrain",
            dependencies: ["SecondBrainCore"],
            path: "Sources/SecondBrain"
        ),
        .target(
            name: "SecondBrainCore",
            dependencies: [
                .product(name: "GRDB", package: "GRDB.swift"),
                .product(name: "Yams", package: "Yams"),
                .product(name: "Markdown", package: "swift-markdown"),
            ],
            path: "Sources/SecondBrainCore"
        ),
        .testTarget(
            name: "SecondBrainCoreTests",
            dependencies: ["SecondBrainCore"],
            path: "Tests/SecondBrainCoreTests"
        ),
        .testTarget(
            name: "SecondBrainAppTests",
            dependencies: ["SecondBrainCore"],
            path: "Tests/SecondBrainAppTests"
        ),
    ]
)
