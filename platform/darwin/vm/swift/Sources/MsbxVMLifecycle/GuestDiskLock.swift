import Darwin
import Foundation

public enum GuestDiskLockError: LocalizedError, Equatable {
    case alreadyLocked
    case systemError(String)

    public var errorDescription: String? {
        switch self {
        case .alreadyLocked:
            return "guest disk is already in use by another msbx VM operation"
        case .systemError(let message):
            return "unable to lock guest disk: \(message)"
        }
    }
}

public final class GuestDiskLock {
    private let fileDescriptor: Int32

    public init(diskPath: String) throws {
        let diskURL = URL(fileURLWithPath: diskPath)
            .standardizedFileURL
            .resolvingSymlinksInPath()
        let lockName = ".\(diskURL.lastPathComponent).msbx.lock"
        let lockURL = diskURL.deletingLastPathComponent().appendingPathComponent(lockName)

        let descriptor = Darwin.open(
            lockURL.path,
            O_CREAT | O_RDWR | O_NOFOLLOW | O_EXLOCK | O_NONBLOCK,
            mode_t(S_IRUSR | S_IWUSR)
        )
        guard descriptor >= 0 else {
            if errno == EWOULDBLOCK || errno == EAGAIN {
                throw GuestDiskLockError.alreadyLocked
            }
            throw GuestDiskLockError.systemError(String(cString: strerror(errno)))
        }

        guard Darwin.fchmod(descriptor, mode_t(S_IRUSR | S_IWUSR)) == 0 else {
            let message = String(cString: strerror(errno))
            _ = Darwin.close(descriptor)
            throw GuestDiskLockError.systemError(message)
        }

        fileDescriptor = descriptor
    }

    deinit {
        _ = Darwin.close(fileDescriptor)
    }
}
