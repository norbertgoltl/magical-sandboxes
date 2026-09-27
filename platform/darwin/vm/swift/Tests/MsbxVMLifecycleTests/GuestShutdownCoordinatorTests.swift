import Foundation
import XCTest

@testable import MsbxVMLifecycle

final class GuestShutdownCoordinatorTests: XCTestCase {
    func testGuestDiskLockRejectsConcurrentLockAndAllowsReuseAfterRelease() throws {
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let diskPath = directory.appendingPathComponent("disk.raw").path
        _ = FileManager.default.createFile(atPath: diskPath, contents: Data())

        do {
            let firstLock = try GuestDiskLock(diskPath: diskPath)
            XCTAssertThrowsError(try GuestDiskLock(diskPath: diskPath)) { error in
                XCTAssertEqual(error as? GuestDiskLockError, .alreadyLocked)
            }
            withExtendedLifetime(firstLock) {}
        }

        XCTAssertNoThrow(try GuestDiskLock(diskPath: diskPath))
    }

    func testShellProtocolMagicUsesCurrentVersion() {
        XCTAssertEqual(ShellProtocol.magic, "MSBX/8")
    }

    func testGracefulShutdownPreservesSuccessfulProcessStatus() {
        let coordinator = GuestShutdownCoordinator()
        coordinator.beginManagedVM()
        coordinator.awaitGuestStop(exitStatus: 0)

        XCTAssertEqual(coordinator.phase, .awaitingGuestStop)
        XCTAssertEqual(coordinator.guestDidStop(), 0)
        XCTAssertEqual(coordinator.phase, .finished)
    }

    func testGracefulShutdownPreservesFailedProcessStatus() {
        let coordinator = GuestShutdownCoordinator()
        coordinator.beginManagedVM()
        coordinator.awaitGuestStop(exitStatus: 23)

        XCTAssertEqual(coordinator.guestDidStop(), 23)
    }

    func testGuestStoppingBeforeExitStatusIsFailure() {
        let coordinator = GuestShutdownCoordinator()
        coordinator.beginManagedVM()

        XCTAssertEqual(coordinator.guestDidStop(), 1)
    }

    func testForcedShutdownStartsOnlyOnceAndReturnsFailure() {
        let coordinator = GuestShutdownCoordinator()
        coordinator.beginManagedVM()
        coordinator.awaitGuestStop(exitStatus: 23)

        XCTAssertTrue(coordinator.beginForcedShutdown())
        XCTAssertFalse(coordinator.beginForcedShutdown())
        XCTAssertEqual(coordinator.phase, .forceStopping)
        XCTAssertEqual(coordinator.forceStopCompleted(), 1)
    }

    func testGuestStopDuringForcedShutdownReturnsFailure() {
        let coordinator = GuestShutdownCoordinator()
        coordinator.beginManagedVM()
        coordinator.awaitGuestStop(exitStatus: 0)
        XCTAssertTrue(coordinator.beginForcedShutdown())

        XCTAssertEqual(coordinator.guestDidStop(), 1)
    }

    func testInvalidProcessStatusBecomesFailure() {
        let coordinator = GuestShutdownCoordinator()
        coordinator.beginManagedVM()
        coordinator.awaitGuestStop(exitStatus: 256)

        XCTAssertEqual(coordinator.guestDidStop(), 1)
    }
}
