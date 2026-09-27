// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "MsbxVM",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "msbx-vm", targets: ["MsbxVM"])
    ],
    targets: [
        .target(name: "MsbxVMLifecycle"),
        .executableTarget(
            name: "MsbxVM",
            dependencies: ["MsbxVMLifecycle"],
            path: "Sources/MsbxVM"
        ),
        .testTarget(
            name: "MsbxVMLifecycleTests",
            dependencies: ["MsbxVMLifecycle"]
        ),
    ]
)
