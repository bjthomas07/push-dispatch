// swift-tools-version: 6.1

import PackageDescription

let package = Package(
    name: "PushDispatch",
    platforms: [
        .iOS(.v15),
    ],
    products: [
        .library(
            name: "PushDispatchCore",
            targets: ["PushDispatchCore"]
        ),
        .library(
            name: "PushDispatchFirebase",
            targets: ["PushDispatchFirebase"]
        ),
    ],
    dependencies: [
        .package(
            url: "https://github.com/firebase/firebase-ios-sdk",
            exact: "12.16.0"
        ),
    ],
    targets: [
        .target(
            name: "PushDispatchCore",
            path: "apple/Sources/PushDispatchCore",
            resources: [
                .process("PrivacyInfo.xcprivacy"),
            ]
        ),
        .target(
            name: "PushDispatchFirebase",
            dependencies: [
                "PushDispatchCore",
                .product(
                    name: "FirebaseMessaging",
                    package: "firebase-ios-sdk"
                ),
            ],
            path: "apple/Sources/PushDispatchFirebase"
        ),
        .testTarget(
            name: "PushDispatchCoreTests",
            dependencies: ["PushDispatchCore"],
            path: "apple/Tests/PushDispatchCoreTests"
        ),
        .testTarget(
            name: "PushDispatchFirebaseTests",
            dependencies: [
                "PushDispatchCore",
                "PushDispatchFirebase",
            ],
            path: "apple/Tests/PushDispatchFirebaseTests"
        ),
    ],
    swiftLanguageModes: [.v5]
)
