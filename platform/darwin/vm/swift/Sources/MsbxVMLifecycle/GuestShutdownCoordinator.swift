import Foundation

public final class GuestShutdownCoordinator {
    public enum Phase: Equatable {
        case inactive
        case starting
        case awaitingGuestStop
        case forceStopping
        case finished
    }

    private let lock = NSLock()
    private var currentPhase: Phase = .inactive
    private var processExitStatus: Int32 = 1

    public init() {}

    public var phase: Phase {
        lock.lock()
        defer { lock.unlock() }
        return currentPhase
    }

    public func beginManagedVM() {
        lock.lock()
        currentPhase = .starting
        processExitStatus = 1
        lock.unlock()
    }

    public func awaitGuestStop(exitStatus: Int32) {
        lock.lock()
        defer { lock.unlock() }
        guard currentPhase == .starting else { return }
        processExitStatus = (0...255).contains(exitStatus) ? exitStatus : 1
        currentPhase = .awaitingGuestStop
    }

    @discardableResult
    public func beginForcedShutdown() -> Bool {
        lock.lock()
        defer { lock.unlock() }
        guard currentPhase == .awaitingGuestStop else { return false }
        processExitStatus = 1
        currentPhase = .forceStopping
        return true
    }

    public func guestDidStop() -> Int32 {
        lock.lock()
        defer { lock.unlock() }

        let status: Int32
        switch currentPhase {
        case .inactive:
            status = 0
        case .awaitingGuestStop:
            status = processExitStatus
        case .starting, .forceStopping, .finished:
            status = 1
        }
        currentPhase = .finished
        return status
    }

    public func forceStopCompleted() -> Int32 {
        lock.lock()
        defer { lock.unlock() }
        currentPhase = .finished
        processExitStatus = 1
        return 1
    }

    public func vmStoppedWithError() -> Int32 {
        lock.lock()
        defer { lock.unlock() }
        currentPhase = .finished
        processExitStatus = 1
        return 1
    }
}
