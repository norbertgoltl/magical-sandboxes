# How magical-sandboxes works

This document describes the `magical-sandboxes` host tools, VM lifecycle, guest agent, project sharing, authentication, data storage, and operational workflows. For detailed troubleshooting of Codex, Claude Code, and OpenCode, see the [Codex](codex.md), [Claude Code](claude.md), and [OpenCode](opencode.md) documents. Their Hungarian versions are [Codex](codex.hu.md), [Claude Code](claude.hu.md), and [OpenCode](opencode.hu.md).

## What is msbx for?

`msbx` is a macOS command-line tool that starts a Linux ARM64 virtual machine using Apple's Virtualization.framework. In the guest, the user can run a shell or one of the supported AI harnesses. The VM has its own Linux disk and Docker Engine; the selected host project is mounted as a writable VirtioFS share.

The goal is to run the harness in a separate guest environment while editing project files directly on the host. Harness sign-ins and settings are stored in each project's guest HOME.

## Requirements and components

### Host

- macOS on Apple Silicon (`arm64`).
- Xcode Command Line Tools and Swift to build the Virtualization.framework helper.
- Go to build `msbx` and the Linux guest agent from source.
- The VM helper must have the `com.apple.security.virtualization` entitlement. Bootstrap signs and verifies it.
- Network access to download the Debian image, guest packages, and harness installers.

### Main programs

| File | Responsibility |
| --- | --- |
| `bin/msbx` | User-facing CLI, project path validation, VM-manager startup, and guest-session relay. |
| `bin/msbx-vm` | Swift helper that configures and starts the Virtualization.framework VM and manages terminal and vsock connections. |
| `bin/msbx-agent-linux-arm64` | Linux guest agent binary compiled from source. |

## Installation and preparation

Run from the repository root:

```sh
./scripts/bootstrap.sh
./bin/msbx doctor
./scripts/prepare-cloud-guest.sh
./scripts/provision.sh
```

`bootstrap.sh` checks the macOS/arm64 and Xcode Command Line Tools requirements, builds and signs the Swift VM helper with the Virtualization entitlement, and, if Go is available, builds the host programs and Linux ARM64 guest agent. `doctor` checks these host prerequisites; it does not install dependencies.

`prepare-cloud-guest.sh` downloads the Debian 13 generic ARM64 cloud image, verifies it with SHA-512, then creates a virtual disk that can grow to 12 GiB and a cloud-init seed. `provision.sh` boots this disk with cloud-init configuration. It creates the guest user and agent service, installs Docker Engine, then installs Codex, Claude Code, and OpenCode in sequence. Long-running installations have timeouts, and logs are written to the serial console and guest log. If an installation fails, the remaining provisioning stops and the VM shuts down.

The clean provisioned template and per-project guest disks remain under `.msbx-dev/sandboxes/` in the repository. The template is cloned when a project is initialized; each resulting project disk and EFI variable store persist independently.

## Commands and workflow

```text
msbx doctor
msbx version
msbx init
msbx status
msbx delete
msbx shell
msbx run codex [harness args...]
msbx run claude [harness args...]
msbx run opencode [harness args...]
```

Run `init`, `shell`, `delete`, and `run` from a separate target project directory:

```sh
cd ~/Work/my-project
/path/to/magical-sandboxes/bin/msbx init
/path/to/magical-sandboxes/bin/msbx shell
```

`init` creates the project's VM if needed. `status` reports whether the current project's VM is starting, running, or stopped, along with sandbox metadata, guest disk and EFI sizes, template readiness, and manager log update time; it does not start the VM. The running state is determined from the guest disk lock. The first `run` or `shell` starts the project VM manager; later sessions connect to that running VM. `run` launches the selected harness and forwards extra arguments. `shell` starts an interactive login Bash shell. `delete` permanently removes only the current project's guest disk and state after interactive confirmation; it refuses while that VM is running. Harnesses use native guest sign-in and settings, isolated per project.

## VM and guest lifecycle

`msbx run`, `msbx shell`, `msbx init`, `msbx status`, and `msbx delete` determine the installation root from the location of the running `msbx` binary. The current working directory is resolved to a canonical path, and the program refuses to use it if it overlaps the msbx installation tree. This prevents the writable guest share from exposing or modifying the msbx installation itself. `msbx init` hashes the canonical project path with SHA-256 and creates that project's VM files under `.msbx-dev/sandboxes/projects/<project-id>/` by cloning the clean prepared template in `.msbx-dev/sandboxes/template/`. Metadata records the full project path so the hashed VM directory can be identified. `msbx delete` removes only the current project's initialized VM after showing its path and receiving interactive confirmation; it refuses while that project's VM disk is locked.

The persistent VM manager holds an exclusive, non-blocking lock on the selected guest disk for the VM lifetime. Different projects use different disks and can run concurrently; sessions within one project share the one manager and VM. The lock file may remain in the sandbox directory, but the operating system owns the actual lock, so an unexpected helper exit does not leave a stale lock. Initialization also holds a project-specific lifecycle lock while cloning the disk and EFI state.

**Concurrency behavior:** initialized projects have independent guest disks, EFI state, guest HOME, and native sign-in state. Their VMs may run concurrently. The guest agent maintains an idle worker and adds workers on demand as sessions arrive; there is no configured session-count cap, while actual concurrency is limited by VM and Mac resources. After the last active session exits, the manager waits three seconds before asking the guest agent to shut down the VM. A new session during this idle grace reuses the running VM. The project ID is derived from its canonical path, so moving or renaming a project creates a separate sandbox for the new path; each sandbox remains bound to the path used to create it.

The VM currently has 4 CPUs and 4096 MiB of memory, NAT networking, a Virtio block device, a serial console, a Virtio socket, and one writable VirtioFS share. The share is labeled `msbx-project`; the guest agent mounts it at the same absolute path sent by the host and runs the command in that directory.

The systemd-launched `msbx-agent` runs as root in the guest. It maintains an idle session worker and starts another when a session is accepted; session workers mount the project on demand and create one terminal PTY per session. A separate control worker listens for the manager's shutdown request. The actual shell and harness command run as the Linux `msbx` user. Provisioning matches this user's UID to the host UID and adds the user to the `docker` group. The `msbx` user has no sudo access.

The helper accepts guest session workers on Virtio-vsock port 4050 and a separate shutdown control worker on port 4052. The session protocol is MSBX/8. The request contains the working directory, terminal settings, command, and arguments. Each host terminal is relayed to a separate guest PTY. On client disconnect, the guest agent sends SIGHUP to the session process group. The manager requests guest shutdown only after the final session exits.

## Storage and state lifetime

| Location | Lifetime and contents |
| --- | --- |
| `.msbx-dev/guest/` | Downloaded Debian image and preparation files. |
| `.msbx-dev/sandboxes/template/` | Clean provisioned guest template; shared as the source for new project VM disks, not used directly for sessions. |
| `.msbx-dev/sandboxes/projects/<project-id>/disk.raw` | Persistent, project-specific Linux root disk. Guest HOME and sandbox-local data stored there persist for that project. |
| `.msbx-dev/sandboxes/projects/<project-id>/efi-vars.fd` | Project-specific VM EFI variable store. |
| Host target project directory | The current working directory, shared with the guest as RW VirtioFS. |
| Guest `/home/msbx` | Per-project persistent HOME with native harness sign-ins and settings. |
| Guest `/run/msbx/` | Runtime RAM-backed files, removed when the guest shuts down. |

Each project share uses the host filesystem, so its contents do not disappear when its VM shuts down. Guest HOME, native sign-ins, and settings under `/home/msbx` are stored on that project's virtual disk.

## Authentication and state

Sign in through each harness inside the guest. Every project VM has its own persistent guest HOME and native login. Deleting a project VM deletes that project's guest sign-in and settings.

## Diagnostics and troubleshooting

On the host:

```sh
./bin/msbx doctor
./bin/msbx version
```

`doctor` checks the macOS/arm64 environment, Xcode Command Line Tools, Swift, VM helper availability, and the Virtualization entitlement. Missing Go is a warning if the compiled binaries are already present; Go is required to bootstrap from source.

In the guest shell, check the running user and Docker access:

```sh
whoami
id
groups
docker version
mountpoint /path/to/project
```

For a startup or shutdown failure, first identify the affected layer: host CLI, VM helper, guest cloud-init/provisioning, guest agent, harness wrapper, or project share. Provisioning logs are in the guest's `/var/log/msbx-provision.log` and on the VM serial console; harness wrapper errors appear directly in the terminal. Use the three dedicated harness documents for harness-specific state checks.

Common causes:

- **`msbx-vm`, guest disk, or agent is missing:** run bootstrap, guest preparation, and provisioning; the command reports the missing path.
- **Launch rejected for a restricted path:** leave the msbx repository and run `shell` or `run` from a separate target project.
- **Guest agent does not mount the project or there is no shell:** check the guest provisioning log, the systemd agent service, and the VirtioFS mount.
- **Docker is unavailable in the guest:** check whether the Docker provisioning step succeeded and whether the guest daemon is running. The harness inherits the `msbx` user's group membership.
- **Harness asks to sign in:** sign in from the harness inside the guest. Verify the project path and project VM disk if the sign-in does not persist.
- **Guest data disappeared:** distinguish the persistent guest disk from temporary files under `/run`. Deleting the guest disk loses native sign-ins and settings.

## Current limitations

- The current host target is macOS on Apple Silicon; the guest is Debian 13 ARM64.
- Guest network access uses NAT; no project-level network policy control is described here.
- The VM control agent runs with root privileges because it mounts VirtioFS and requests shutdown. The user shell and harness process do not run as root.
- The three harness installers and authentication flows differ; they are documented separately.
- Session-worker count grows on demand; practical concurrency is limited by available VM and host CPU and memory.
