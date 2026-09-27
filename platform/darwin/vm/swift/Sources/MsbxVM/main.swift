import Darwin
import Foundation
import MsbxVMLifecycle
import Virtualization

private final class ConsoleInputBridge {
    let readHandle: FileHandle
    private let writeHandle: FileHandle
    private var pending = Data()

    init(source: FileHandle = .standardInput) {
        let pipe = Pipe()
        self.readHandle = pipe.fileHandleForReading
        self.writeHandle = pipe.fileHandleForWriting

        source.readabilityHandler = { [weak self, weak source] handle in
            let data = handle.availableData
            guard !data.isEmpty else {
                source?.readabilityHandler = nil
                try? self?.writeHandle.close()
                return
            }
            self?.forwardFilteringTerminalReplies(data)
        }
    }

    deinit {
        FileHandle.standardInput.readabilityHandler = nil
        try? writeHandle.close()
        try? readHandle.close()
    }

    private func forwardFilteringTerminalReplies(_ data: Data) {
        pending.append(data)
        var output = Data()
        var index = pending.startIndex

        while index < pending.endIndex {
            let byte = pending[index]
            guard byte == 0x1b else {
                output.append(byte)
                index = pending.index(after: index)
                continue
            }

            let escapeStart = index
            let next = pending.index(after: index)
            guard next < pending.endIndex else { break }

            guard pending[next] == 0x5b else {
                output.append(byte)
                index = next
                continue
            }

            var cursor = pending.index(after: next)
            var finalIndex: Data.Index?
            while cursor < pending.endIndex {
                let candidate = pending[cursor]
                if candidate >= 0x40 && candidate <= 0x7e {
                    finalIndex = cursor
                    break
                }
                cursor = pending.index(after: cursor)
            }

            guard let finalIndex else { break }
            let finalByte = pending[finalIndex]
            let sequenceEnd = pending.index(after: finalIndex)

            if finalByte != 0x52 {  // 'R' = cursor position report (CPR)
                output.append(pending[escapeStart..<sequenceEnd])
            }
            index = sequenceEnd
        }

        if index > pending.startIndex {
            pending.removeSubrange(pending.startIndex..<index)
        }

        if !output.isEmpty {
            do {
                try writeHandle.write(contentsOf: output)
            } catch {
                // VM shutdown can close the pipe while stdin is still readable.
            }
        }
    }
}

private final class ConsoleOutputBridge {
    let writeHandle: FileHandle
    private let readHandle: FileHandle
    private var pending = Data()

    init(destination: FileHandle = .standardOutput) {
        let pipe = Pipe()
        self.readHandle = pipe.fileHandleForReading
        self.writeHandle = pipe.fileHandleForWriting

        readHandle.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty else {
                handle.readabilityHandler = nil
                return
            }
            self?.forwardFilteringTerminalQueries(data, destination: destination)
        }
    }

    deinit {
        readHandle.readabilityHandler = nil
        try? writeHandle.close()
        try? readHandle.close()
    }

    // The Linux serial console occasionally asks the attached terminal for a
    // device-status/cursor-position report (CSI 5 n / CSI 6 n). A real terminal
    // answers those requests by writing bytes back to its TTY input queue. That
    // is useful for a directly attached terminal, but wrong for our serial-log
    // bridge: the reply can leak into the guest input or remain queued for the
    // host shell after the VM exits. Strip only those query sequences while
    // preserving normal ANSI formatting/control output.
    private func forwardFilteringTerminalQueries(_ data: Data, destination: FileHandle) {
        pending.append(data)
        var output = Data()
        var index = pending.startIndex

        while index < pending.endIndex {
            guard pending[index] == 0x1b else {
                output.append(pending[index])
                index = pending.index(after: index)
                continue
            }

            let escapeStart = index
            let bracket = pending.index(after: index)
            guard bracket < pending.endIndex else { break }
            guard pending[bracket] == 0x5b else {  // '['
                output.append(pending[index])
                index = bracket
                continue
            }

            var cursor = pending.index(after: bracket)
            var finalIndex: Data.Index?
            while cursor < pending.endIndex {
                let byte = pending[cursor]
                if byte >= 0x40 && byte <= 0x7e {
                    finalIndex = cursor
                    break
                }
                cursor = pending.index(after: cursor)
            }

            guard let finalIndex else { break }
            let end = pending.index(after: finalIndex)
            let finalByte = pending[finalIndex]
            let bodyStart = pending.index(after: bracket)
            let body = pending[bodyStart..<finalIndex]

            let isDSRQuery: Bool
            if finalByte == 0x6e {  // 'n'
                let bytes = Array(body)
                isDSRQuery = bytes == [0x35] || bytes == [0x36] || bytes == [0x3f, 0x36]
            } else {
                isDSRQuery = false
            }

            if !isDSRQuery {
                output.append(pending[escapeStart..<end])
            }
            index = end
        }

        if index > pending.startIndex {
            pending.removeSubrange(pending.startIndex..<index)
        }

        if !output.isEmpty {
            do {
                try destination.write(contentsOf: output)
            } catch {
                // VM shutdown can close stdout while console data is draining.
            }
        }
    }
}

private enum ExitCode: Int32 {
    case failure = 1
    case usage = 2
}

private final class VMDelegate: NSObject, VZVirtualMachineDelegate {
    func guestDidStop(_ virtualMachine: VZVirtualMachine) {
        let status = MsbxVM.shutdownCoordinator.guestDidStop()
        fputs("\r\n[msbx-vm] guest stopped (exit status \(status))\r\n", stderr)
        exit(status)
    }
    func virtualMachine(_ virtualMachine: VZVirtualMachine, didStopWithError error: Error) {
        fputs("\n[msbx-vm] VM stopped with error: \(error)\n", stderr)
        exit(MsbxVM.shutdownCoordinator.vmStoppedWithError())
    }
}

private final class ProjectAgentPool {
    private let condition = NSCondition()
    private var available: [VZVirtioSocketConnection] = []
    private var controlConnection: VZVirtioSocketConnection?
    private var activeSessions = 0
    private var hasHadSession = false
    private var isStopping = false
    private var idleGeneration = 0
    private let vm: VZVirtualMachine

    init(vm: VZVirtualMachine) { self.vm = vm }

    func addWorker(_ connection: VZVirtioSocketConnection) {
        condition.lock()
        if !isStopping {
            available.append(connection)
            condition.broadcast()
        }
        condition.unlock()
    }

    func setControl(_ connection: VZVirtioSocketConnection) {
        condition.lock()
        controlConnection = connection
        condition.broadcast()
        condition.unlock()
    }

    func beginSession() -> Bool {
        condition.lock()
        defer { condition.unlock() }
        guard !isStopping else { return false }
        activeSessions += 1
        hasHadSession = true
        idleGeneration += 1
        condition.broadcast()
        return true
    }

    func takeWorker() -> VZVirtioSocketConnection? {
        condition.lock()
        defer { condition.unlock() }
        while available.isEmpty && !isStopping { condition.wait() }
        guard !isStopping else { return nil }
        return available.removeFirst()
    }

    func endSession() {
        condition.lock()
        activeSessions = max(0, activeSessions - 1)
        guard activeSessions == 0 && hasHadSession && !isStopping else {
            condition.unlock()
            return
        }
        idleGeneration += 1
        let generation = idleGeneration
        condition.unlock()
        DispatchQueue.global().asyncAfter(deadline: .now() + 3.0) { [weak self] in
            self?.stopIfIdle(generation: generation)
        }
    }

    private func stopIfIdle(generation: Int) {
        condition.lock()
        guard activeSessions == 0 && !isStopping && idleGeneration == generation else {
            condition.unlock()
            return
        }
        isStopping = true
        let connection = controlConnection
        condition.broadcast()
        condition.unlock()

        MsbxVM.shutdownCoordinator.awaitGuestStop(exitStatus: 0)
        if let connection {
            if !MsbxVM.writeAll(fd: connection.fileDescriptor, data: Data("STOP\n".utf8)) {
                try? vm.requestStop()
            }
        } else {
            try? vm.requestStop()
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 8.0) {
            guard MsbxVM.shutdownCoordinator.phase == .awaitingGuestStop else { return }
            if self.vm.state == .stopped {
                exit(MsbxVM.shutdownCoordinator.guestDidStop())
            }
            guard MsbxVM.shutdownCoordinator.beginForcedShutdown() else { return }
            fputs("\r\nmsbx-vm: idle guest did not power off; forcing VM stop\n", stderr)
            if self.vm.canStop {
                self.vm.stop { error in
                    if let error { fputs("msbx-vm: forced stop failed: \(error)\n", stderr) }
                    exit(MsbxVM.shutdownCoordinator.forceStopCompleted())
                }
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + 2.0) {
                guard MsbxVM.shutdownCoordinator.phase == .forceStopping else { return }
                fputs("msbx-vm: VM did not stop after forced shutdown\n", stderr)
                exit(MsbxVM.shutdownCoordinator.forceStopCompleted())
            }
        }
    }
}

private final class AgentSocketDelegate: NSObject, VZVirtioSocketListenerDelegate {
    private let onConnection: (VZVirtioSocketConnection) -> Void
    init(onConnection: @escaping (VZVirtioSocketConnection) -> Void) {
        self.onConnection = onConnection
    }
    func listener(
        _ listener: VZVirtioSocketListener,
        shouldAcceptNewConnection connection: VZVirtioSocketConnection,
        from socketDevice: VZVirtioSocketDevice
    ) -> Bool {
        DispatchQueue.global(qos: .userInitiated).async { [onConnection] in onConnection(connection)
        }
        return true
    }
}

private final class LocalSessionListener {
    private let socketPath: String
    private let pool: ProjectAgentPool
    private var descriptor: Int32 = -1
    private var source: DispatchSourceRead?

    init(socketPath: String, pool: ProjectAgentPool) throws {
        self.socketPath = socketPath
        self.pool = pool
        descriptor = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
        _ = fcntl(descriptor, F_SETFL, O_NONBLOCK)
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = socketPath.utf8CString.map { UInt8(bitPattern: $0) }
        let capacity = MemoryLayout.size(ofValue: address.sun_path)
        guard pathBytes.count <= capacity else {
            throw NSError(
                domain: "msbx", code: 1,
                userInfo: [NSLocalizedDescriptionKey: "local socket path is too long"])
        }
        withUnsafeMutableBytes(of: &address.sun_path) { $0.copyBytes(from: pathBytes) }
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        let bindResult = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.bind(descriptor, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard bindResult == 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
        guard Darwin.listen(descriptor, 64) == 0 else {
            throw POSIXError(.init(rawValue: errno) ?? .EIO)
        }
        _ = Darwin.chmod(socketPath, mode_t(S_IRUSR | S_IWUSR))
        let readSource = DispatchSource.makeReadSource(
            fileDescriptor: descriptor, queue: DispatchQueue.global(qos: .userInitiated))
        readSource.setEventHandler { [weak self] in self?.acceptAvailable() }
        readSource.setCancelHandler { [descriptor] in _ = Darwin.close(descriptor) }
        source = readSource
        readSource.resume()
    }

    private func acceptAvailable() {
        while true {
            let client = Darwin.accept(descriptor, nil, nil)
            guard client >= 0 else {
                if errno == EINTR { continue }
                return
            }
            _ = fcntl(client, F_SETFL, 0)
            DispatchQueue.global(qos: .userInteractive).async { [pool] in
                MsbxVM.serveLocalSession(clientFD: client, pool: pool)
                _ = Darwin.close(client)
            }
        }
    }
}

@main
struct MsbxVM {
    static func main() {
        let args = Array(CommandLine.arguments.dropFirst())
        guard let command = args.first else { usage() }
        switch command {
        case "probe": probe()
        case "provision-cloud": provisionCloud(Array(args.dropFirst()))
        case "boot-disk": bootDisk(Array(args.dropFirst()))
        case "serve": serve(Array(args.dropFirst()))
        default: usage()
        }
    }

    private static func probe() {
        guard VZVirtualMachine.isSupported else {
            fail("virtualization is not supported on this Mac")
        }
        #if arch(arm64)
            print("supported (arm64)")
        #else
            print("unsupported-architecture")
        #endif
    }

    private static func provisionCloud(_ args: [String]) {
        guard VZVirtualMachine.isSupported else {
            fail("virtualization is not supported on this Mac")
        }
        var diskPath: String?
        var seedPath: String?
        var efiStorePath: String?
        var cpuCount = 2
        var memoryMiB: UInt64 = 2048
        var index = 0
        while index < args.count {
            switch args[index] {
            case "--disk":
                index += 1
                guard index < args.count else { usage() }
                diskPath = args[index]
            case "--seed":
                index += 1
                guard index < args.count else { usage() }
                seedPath = args[index]
            case "--efi-store":
                index += 1
                guard index < args.count else { usage() }
                efiStorePath = args[index]
            case "--cpus":
                index += 1
                guard index < args.count, let v = Int(args[index]), v > 0 else {
                    fail("--cpus must be positive")
                }
                cpuCount = v
            case "--memory-mib":
                index += 1
                guard index < args.count, let v = UInt64(args[index]), v >= 1024 else {
                    fail("--memory-mib must be at least 1024")
                }
                memoryMiB = v
            default: fail("unknown provision-cloud option: \(args[index])")
            }
            index += 1
        }
        guard let diskPath, let seedPath, let efiStorePath else { usage() }
        let diskURL = URL(fileURLWithPath: diskPath)
        let seedURL = URL(fileURLWithPath: seedPath)
        let efiURL = URL(fileURLWithPath: efiStorePath)
        for url in [diskURL, seedURL] where !FileManager.default.fileExists(atPath: url.path) {
            fail("file not found: \(url.path)")
        }
        acquireGuestDiskLock(diskPath: diskPath)
        do {
            try FileManager.default.createDirectory(
                at: efiURL.deletingLastPathComponent(), withIntermediateDirectories: true)
        } catch { fail("unable to create EFI directory: \(error)") }

        let configuration = baseConfiguration(cpuCount: cpuCount, memoryMiB: memoryMiB)
        let bootLoader = VZEFIBootLoader()
        do {
            bootLoader.variableStore =
                FileManager.default.fileExists(atPath: efiURL.path)
                ? VZEFIVariableStore(url: efiURL)
                : try VZEFIVariableStore(creatingVariableStoreAt: efiURL)
        } catch { fail("unable to initialize EFI store: \(error)") }
        configuration.bootLoader = bootLoader
        do {
            let diskAttachment = try VZDiskImageStorageDeviceAttachment(
                url: diskURL, readOnly: false)
            let seedAttachment = try VZDiskImageStorageDeviceAttachment(
                url: seedURL, readOnly: true)
            configuration.storageDevices = [
                VZVirtioBlockDeviceConfiguration(attachment: diskAttachment),
                VZVirtioBlockDeviceConfiguration(attachment: seedAttachment),
            ]
        } catch { fail("unable to attach cloud disk/seed: \(error)") }
        configuration.serialPorts = [serialConsole()]
        configuration.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]
        configuration.networkDevices = [natNetwork()]

        print("[msbx-vm] provisioning Debian cloud guest")
        print("[msbx-vm] disk: \(diskURL.path)")
        print("[msbx-vm] seed: \(seedURL.path)")
        print("[msbx-vm] cloud-init will power off the guest when provisioning finishes")
        validateAndRun(configuration, message: "Debian cloud provisioning VM")
    }

    private static func bootDisk(_ args: [String]) {
        guard VZVirtualMachine.isSupported else {
            fail("virtualization is not supported on this Mac")
        }
        var diskPath: String?
        var efiStorePath: String?
        var sharePath: String?
        var shareTag = "msbx-project"
        var cpuCount = 2
        var memoryMiB: UInt64 = 2048
        var index = 0
        while index < args.count {
            switch args[index] {
            case "--disk":
                index += 1
                guard index < args.count else { usage() }
                diskPath = args[index]
            case "--efi-store":
                index += 1
                guard index < args.count else { usage() }
                efiStorePath = args[index]
            case "--share":
                index += 1
                guard index < args.count else { usage() }
                sharePath = args[index]
            case "--share-tag":
                index += 1
                guard index < args.count else { usage() }
                shareTag = args[index]
            case "--cpus":
                index += 1
                guard index < args.count, let v = Int(args[index]), v > 0 else {
                    fail("--cpus must be positive")
                }
                cpuCount = v
            case "--memory-mib":
                index += 1
                guard index < args.count, let v = UInt64(args[index]), v >= 1024 else {
                    fail("--memory-mib must be at least 1024")
                }
                memoryMiB = v
            default: fail("unknown boot-disk option: \(args[index])")
            }
            index += 1
        }
        guard let diskPath, let efiStorePath else { usage() }
        let diskURL = URL(fileURLWithPath: diskPath)
        let efiURL = URL(fileURLWithPath: efiStorePath)
        guard FileManager.default.fileExists(atPath: diskURL.path) else {
            fail("disk not found: \(diskURL.path)")
        }
        acquireGuestDiskLock(diskPath: diskPath)
        do {
            try FileManager.default.createDirectory(
                at: efiURL.deletingLastPathComponent(), withIntermediateDirectories: true)
        } catch { fail("unable to create EFI directory: \(error)") }

        let configuration = baseConfiguration(cpuCount: cpuCount, memoryMiB: memoryMiB)
        let bootLoader = VZEFIBootLoader()
        do {
            bootLoader.variableStore =
                FileManager.default.fileExists(atPath: efiURL.path)
                ? VZEFIVariableStore(url: efiURL)
                : try VZEFIVariableStore(creatingVariableStoreAt: efiURL)
        } catch { fail("unable to initialize EFI store: \(error)") }
        configuration.bootLoader = bootLoader
        do {
            let attachment = try VZDiskImageStorageDeviceAttachment(url: diskURL, readOnly: false)
            configuration.storageDevices = [
                VZVirtioBlockDeviceConfiguration(attachment: attachment)
            ]
        } catch { fail("unable to attach disk: \(error)") }
        configuration.serialPorts = [serialConsole()]
        configuration.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]
        configuration.networkDevices = [natNetwork()]

        if let sharePath {
            let shareURL = URL(fileURLWithPath: sharePath).standardizedFileURL
            var isDirectory: ObjCBool = false
            guard FileManager.default.fileExists(atPath: shareURL.path, isDirectory: &isDirectory),
                isDirectory.boolValue
            else {
                fail("share path is not a directory: \(shareURL.path)")
            }
            let sharedDirectory = VZSharedDirectory(url: shareURL, readOnly: false)
            let share = VZSingleDirectoryShare(directory: sharedDirectory)
            let device = VZVirtioFileSystemDeviceConfiguration(tag: shareTag)
            device.share = share
            configuration.directorySharingDevices = [device]
        }

        print("[msbx-vm] disk: \(diskURL.path)")
        print("[msbx-vm] EFI store: \(efiURL.path)")
        if let sharePath {
            print(
                "[msbx-vm] VirtioFS RW share: \(URL(fileURLWithPath: sharePath).standardizedFileURL.path)"
            )
            print("[msbx-vm] VirtioFS tag: \(shareTag)")
        }
        print("[msbx-vm] press Control-C to stop the VM process\n")
        validateAndRun(configuration, message: "persistent Debian ARM64 VM")
    }

    private static func serve(_ args: [String]) {
        shutdownCoordinator.beginManagedVM()
        signal(SIGPIPE, SIG_IGN)
        guard VZVirtualMachine.isSupported else {
            fail("virtualization is not supported on this Mac")
        }
        var diskPath: String?
        var efiStorePath: String?
        var sharePath: String?
        var socketPath: String?
        var cpuCount = 4
        var memoryMiB: UInt64 = 4096
        var index = 0
        while index < args.count {
            switch args[index] {
            case "--disk":
                index += 1
                guard index < args.count else { usage() }
                diskPath = args[index]
            case "--efi-store":
                index += 1
                guard index < args.count else { usage() }
                efiStorePath = args[index]
            case "--share":
                index += 1
                guard index < args.count else { usage() }
                sharePath = args[index]
            case "--socket":
                index += 1
                guard index < args.count else { usage() }
                socketPath = args[index]
            case "--cpus":
                index += 1
                guard index < args.count, let v = Int(args[index]), v > 0 else {
                    fail("--cpus must be positive")
                }
                cpuCount = v
            case "--memory-mib":
                index += 1
                guard index < args.count, let v = UInt64(args[index]), v >= 1024 else {
                    fail("--memory-mib must be at least 1024")
                }
                memoryMiB = v
            default: fail("unknown serve option: \(args[index])")
            }
            index += 1
        }
        guard let diskPath, let efiStorePath, let sharePath, let socketPath else { usage() }
        let diskURL = URL(fileURLWithPath: diskPath)
        let efiURL = URL(fileURLWithPath: efiStorePath)
        let shareURL = URL(fileURLWithPath: sharePath).standardizedFileURL
        guard FileManager.default.fileExists(atPath: diskURL.path) else {
            fail("disk not found: \(diskURL.path)")
        }
        acquireGuestDiskLock(diskPath: diskPath)
        do {
            try FileManager.default.createDirectory(
                at: efiURL.deletingLastPathComponent(), withIntermediateDirectories: true)
        } catch { fail("unable to create EFI directory: \(error)") }
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: shareURL.path, isDirectory: &isDirectory),
            isDirectory.boolValue
        else {
            fail("share path is not a directory: \(shareURL.path)")
        }

        let configuration = baseConfiguration(cpuCount: cpuCount, memoryMiB: memoryMiB)
        let bootLoader = VZEFIBootLoader()
        do {
            bootLoader.variableStore =
                FileManager.default.fileExists(atPath: efiURL.path)
                ? VZEFIVariableStore(url: efiURL)
                : try VZEFIVariableStore(creatingVariableStoreAt: efiURL)
        } catch { fail("unable to initialize EFI store: \(error)") }
        configuration.bootLoader = bootLoader
        do {
            let attachment = try VZDiskImageStorageDeviceAttachment(url: diskURL, readOnly: false)
            configuration.storageDevices = [
                VZVirtioBlockDeviceConfiguration(attachment: attachment)
            ]
        } catch { fail("unable to attach disk: \(error)") }
        configuration.serialPorts = [silentSerialConsole()]
        configuration.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]
        configuration.networkDevices = [natNetwork()]
        configuration.socketDevices = [VZVirtioSocketDeviceConfiguration()]
        let sharedDirectory = VZSharedDirectory(url: shareURL, readOnly: false)
        let share = VZSingleDirectoryShare(directory: sharedDirectory)
        let fsDevice = VZVirtioFileSystemDeviceConfiguration(tag: "msbx-project")
        fsDevice.share = share
        configuration.directorySharingDevices = [fsDevice]
        do { try configuration.validate() } catch {
            fail("VM configuration validation failed: \(error)")
        }

        do { try FileManager.default.removeItem(atPath: socketPath) } catch {}
        let vm = VZVirtualMachine(configuration: configuration)
        let vmDelegate = VMDelegate()
        vm.delegate = vmDelegate
        guard let device = vm.socketDevices.first as? VZVirtioSocketDevice else {
            fail("Virtio socket device was not created")
        }
        let pool = ProjectAgentPool(vm: vm)
        let sessionDelegate = AgentSocketDelegate { pool.addWorker($0) }
        let sessionListener = VZVirtioSocketListener()
        sessionListener.delegate = sessionDelegate
        device.setSocketListener(sessionListener, forPort: 4050)
        let controlDelegate = AgentSocketDelegate { pool.setControl($0) }
        let controlListener = VZVirtioSocketListener()
        controlListener.delegate = controlDelegate
        device.setSocketListener(controlListener, forPort: 4052)
        let localListener: LocalSessionListener
        do { localListener = try LocalSessionListener(socketPath: socketPath, pool: pool) } catch {
            fail("unable to create project session socket: \(error)")
        }
        print("[msbx-vm] serving project VM with disk \(diskURL.path)")
        print("[msbx-vm] local session socket: \(socketPath)")
        vm.start { result in
            if case .failure(let error) = result { fail("failed to start VM: \(error)") }
        }
        withExtendedLifetime(
            (
                vmDelegate, sessionDelegate, sessionListener, controlDelegate, controlListener,
                localListener, pool
            )
        ) {
            RunLoop.main.run()
        }
    }

    fileprivate static func serveLocalSession(clientFD: Int32, pool: ProjectAgentPool) {
        guard pool.beginSession() else {
            _ = writeAll(fd: clientFD, data: Data("ERROR project VM is shutting down\n".utf8))
            return
        }
        defer { pool.endSession() }
        let limits = [64, 16 * 1024, 1024, 32, 32, 16 * 1024, 64 * 1024]
        var lines: [String] = []
        for limit in limits {
            guard let line = readLine(fd: clientFD, max: limit) else { return }
            lines.append(line)
        }
        guard lines.first == ShellProtocol.magic else {
            _ = writeAll(fd: clientFD, data: Data("ERROR incompatible session protocol\n".utf8))
            return
        }
        guard let worker = pool.takeWorker() else {
            _ = writeAll(
                fd: clientFD, data: Data("ERROR no guest session worker is available\n".utf8))
            return
        }
        let workerFD = worker.fileDescriptor
        let header = Data((lines.joined(separator: "\n") + "\n").utf8)
        guard writeAll(fd: workerFD, data: header) else {
            _ = writeAll(
                fd: clientFD, data: Data("ERROR unable to send guest session request\n".utf8))
            return
        }
        guard let ready = readLine(fd: workerFD, max: 8192) else {
            _ = writeAll(fd: clientFD, data: Data("ERROR guest session worker disconnected\n".utf8))
            return
        }
        guard ready == "READY" else {
            _ = writeAll(fd: clientFD, data: Data((ready + "\n").utf8))
            return
        }
        guard writeAll(fd: clientFD, data: Data("READY\n".utf8)) else { return }

        let inputFinished = DispatchSemaphore(value: 0)
        DispatchQueue.global(qos: .userInitiated).async {
            defer { _ = Darwin.shutdown(workerFD, SHUT_WR) }
            while let frame = readShellFrame(fd: clientFD) {
                guard frame.type == 1 || frame.type == 2,
                    writeFrame(fd: workerFD, type: frame.type, payload: frame.payload)
                else { break }
            }
            inputFinished.signal()
        }
        while let frame = readShellFrame(fd: workerFD) {
            guard frame.type == 3 || frame.type == 4,
                writeFrame(fd: clientFD, type: frame.type, payload: frame.payload)
            else { break }
            if frame.type == 4 { break }
        }
        _ = Darwin.shutdown(clientFD, SHUT_RD)
        _ = Darwin.shutdown(workerFD, SHUT_RDWR)
        _ = inputFinished.wait(timeout: .now() + 1.0)
    }

    private static func writeFrame(fd: Int32, type: UInt8, payload: Data) -> Bool {
        let count = UInt32(payload.count)
        var frame = Data([
            type, UInt8((count >> 24) & 0xff), UInt8((count >> 16) & 0xff),
            UInt8((count >> 8) & 0xff), UInt8(count & 0xff),
        ])
        frame.append(payload)
        return writeAll(fd: fd, data: frame)
    }

    private static func readLine(fd: Int32, max: Int) -> String? {
        var bytes = [UInt8]()
        bytes.reserveCapacity(128)
        var byte: UInt8 = 0
        while bytes.count < max {
            let n = Darwin.read(fd, &byte, 1)
            if n == 1 {
                if byte == 10 {
                    return String(bytes: bytes, encoding: .utf8)?.trimmingCharacters(in: .newlines)
                }
                if byte != 13 { bytes.append(byte) }
            } else if n == 0 {
                return nil
            } else if errno != EINTR {
                return nil
            }
        }
        return nil
    }

    fileprivate static func writeAll(fd: Int32, data: Data) -> Bool {
        return data.withUnsafeBytes { raw in
            guard let base = raw.baseAddress else { return true }
            var offset = 0
            while offset < raw.count {
                let n = Darwin.write(fd, base.advanced(by: offset), raw.count - offset)
                if n > 0 {
                    offset += n
                    continue
                }
                if n < 0 && errno == EINTR { continue }
                return false
            }
            return true
        }
    }

    private static func readShellFrame(fd: Int32) -> (type: UInt8, payload: Data)? {
        guard let header = readExact(fd: fd, count: 5) else { return nil }
        let count = header[1..<5].reduce(UInt32(0)) { ($0 << 8) | UInt32($1) }
        guard count <= 1 << 20, let payload = readExact(fd: fd, count: Int(count)) else {
            return nil
        }
        return (header[0], payload)
    }

    private static func readExact(fd: Int32, count: Int) -> Data? {
        var data = Data(count: count)
        let completed = data.withUnsafeMutableBytes { raw -> Bool in
            guard let base = raw.baseAddress else { return count == 0 }
            var offset = 0
            while offset < count {
                let n = Darwin.read(fd, base.advanced(by: offset), count - offset)
                if n > 0 {
                    offset += n
                    continue
                }
                if n < 0 && errno == EINTR { continue }
                return false
            }
            return true
        }
        return completed ? data : nil
    }

    private static func silentSerialConsole() -> VZVirtioConsoleDeviceSerialPortConfiguration {
        let console = VZVirtioConsoleDeviceSerialPortConfiguration()
        guard let input = FileHandle(forReadingAtPath: "/dev/null"),
            let output = FileHandle(forWritingAtPath: "/dev/null")
        else {
            fail("unable to open /dev/null for serial console")
        }
        console.attachment = VZFileHandleSerialPortAttachment(
            fileHandleForReading: input, fileHandleForWriting: output)
        return console
    }

    private static func natNetwork() -> VZVirtioNetworkDeviceConfiguration {
        let network = VZVirtioNetworkDeviceConfiguration()
        network.attachment = VZNATNetworkDeviceAttachment()
        network.macAddress = VZMACAddress.randomLocallyAdministered()
        return network
    }

    private static func baseConfiguration(cpuCount: Int, memoryMiB: UInt64)
        -> VZVirtualMachineConfiguration
    {
        let c = VZVirtualMachineConfiguration()
        c.platform = VZGenericPlatformConfiguration()
        c.cpuCount = min(
            max(cpuCount, VZVirtualMachineConfiguration.minimumAllowedCPUCount),
            VZVirtualMachineConfiguration.maximumAllowedCPUCount)
        let requested = memoryMiB * 1024 * 1024
        c.memorySize = min(
            max(requested, VZVirtualMachineConfiguration.minimumAllowedMemorySize),
            VZVirtualMachineConfiguration.maximumAllowedMemorySize)
        return c
    }

    private static var consoleInputBridge: ConsoleInputBridge?
    private static var consoleOutputBridge: ConsoleOutputBridge?
    private static var activeGuestDiskLock: GuestDiskLock?
    fileprivate static let shutdownCoordinator = GuestShutdownCoordinator()

    private static func acquireGuestDiskLock(diskPath: String) {
        do {
            activeGuestDiskLock = try GuestDiskLock(diskPath: diskPath)
        } catch {
            fail("\(error.localizedDescription); wait for the active VM operation to finish")
        }
    }

    private static func serialConsole() -> VZVirtioConsoleDeviceSerialPortConfiguration {
        let console = VZVirtioConsoleDeviceSerialPortConfiguration()
        let inputBridge = ConsoleInputBridge()
        let outputBridge = ConsoleOutputBridge()
        consoleInputBridge = inputBridge
        consoleOutputBridge = outputBridge
        console.attachment = VZFileHandleSerialPortAttachment(
            fileHandleForReading: inputBridge.readHandle,
            fileHandleForWriting: outputBridge.writeHandle
        )
        return console
    }

    private static func validateAndRun(
        _ configuration: VZVirtualMachineConfiguration, message: String
    ) {
        do { try configuration.validate() } catch {
            fail("VM configuration validation failed: \(error)")
        }
        print(
            "[msbx-vm] starting \(message) (\(configuration.cpuCount) CPU, \(configuration.memorySize / 1024 / 1024) MiB)"
        )
        let vm = VZVirtualMachine(configuration: configuration)
        let delegate = VMDelegate()
        vm.delegate = delegate
        signal(SIGINT, SIG_DFL)
        vm.start { result in
            if case .failure(let error) = result { fail("failed to start VM: \(error)") }
        }
        withExtendedLifetime(delegate) { RunLoop.main.run() }
    }

    private static func usage() -> Never {
        fputs(
            """
            usage:
              msbx-vm probe
              msbx-vm provision-cloud --disk <raw-image> --seed <seed.iso> --efi-store <path> [--cpus N] [--memory-mib N]
              msbx-vm boot-disk --disk <raw-image> --efi-store <path> [--share <host-directory>] [--share-tag <tag>] [--cpus N] [--memory-mib N]
              msbx-vm serve --disk <raw-image> --efi-store <path> --share <host-directory> --socket <local-socket> [--cpus N] [--memory-mib N]

            """, stderr)
        exit(ExitCode.usage.rawValue)
    }
    private static func fail(_ message: String) -> Never {
        fputs("msbx-vm: \(message)\n", stderr)
        exit(ExitCode.failure.rawValue)
    }
}
