# Magical Sandboxes 0.1.0

`msbx` is a macOS command-line tool that launches a shell or AI harness inside a Debian ARM64 virtual machine. The guest receives the selected project as a writable VirtioFS share; the guest Linux disk persists between runs.

Detailed system overview: [docs/magical-sandboxes.md](docs/magical-sandboxes.md). Harness-specific behavior and troubleshooting: [Codex](docs/codex.md), [Claude Code](docs/claude.md), [OpenCode](docs/opencode.md).

## Requirements

- macOS 14 or later on Apple Silicon (`arm64`)
- Xcode Command Line Tools and Swift 5.9 or later
- Go 1.23 or later
- Network access to download the Debian image, guest packages, and harness installers

The VM helper uses Apple's Virtualization.framework. Each harness uses its own native sign-in and settings inside the project's persistent guest VM. No host-side harness login or credential forwarding is involved.

## Build from scratch

Run the commands from the `magical-sandboxes` repository root.

### 1. Build the host and guest programs

```sh
./scripts/bootstrap.sh
./bin/msbx doctor
```

The bootstrap compiles and signs the `bin/msbx-vm` VM helper with the Virtualization entitlement. It also compiles the `bin/msbx` and `bin/msbx-agent-linux-arm64` programs using Go. `doctor` checks whether the host meets the requirements for starting the VM.

### 2. Prepare and provision the guest VM

```sh
./scripts/prepare-cloud-guest.sh
./scripts/provision.sh
```

The preparation script downloads and checksum-verifies the Debian 13 ARM64 cloud image, then creates the guest disk and cloud-init seed. Provisioning installs Docker Engine, Codex, Claude Code, OpenCode, and the `msbx-agent` service in the guest. Progress is reported on the serial console, and provisioning stops if an installation fails.

The clean, provisioned guest template—the disk, cloud-init seed, EFI state, and provisioning log—is located at:

```text
.msbx-dev/sandboxes/template/
```

This template is the source for project-specific VM disks. It is not used directly for harness sessions.

### 3. Sign in inside each project's VM

Start the harness from a target project directory. On its first launch, use the harness's normal interactive sign-in. Each project has its own guest HOME and sign-in data; another project must sign in separately.

```sh
mkdir -p ~/Work/msbx-test-project
cd ~/Work/msbx-test-project
/path/to/magical-sandboxes/bin/msbx init
/path/to/magical-sandboxes/bin/msbx run codex
```

## Start a shell or harness

Always run the `shell` and `run` commands from a project directory separate from the `magical-sandboxes` repository. `msbx` refuses to start if the path being shared overlaps the msbx installation directory.

```sh
mkdir -p ~/Work/msbx-test-project
cd ~/Work/msbx-test-project

/path/to/magical-sandboxes/bin/msbx init
/path/to/magical-sandboxes/bin/msbx status
/path/to/magical-sandboxes/bin/msbx shell
/path/to/magical-sandboxes/bin/msbx run codex
/path/to/magical-sandboxes/bin/msbx run claude
/path/to/magical-sandboxes/bin/msbx run opencode
```

Harness-specific arguments can follow `run`:

```sh
/path/to/magical-sandboxes/bin/msbx run codex --help
```

Run `msbx status` from this project directory to see whether its VM is running, starting, or stopped. It also reports sandbox initialization, guest disk and EFI state sizes, template readiness, and the manager log's last update time. Status checks the VM's guest-disk lock without starting the VM.

The user in the guest shell is `msbx`. This user has no sudo access but belongs to the `docker` group:

```sh
whoami
id
groups
docker version
```

`msbx init` creates a VM disk and EFI state for the current canonical project path under `.msbx-dev/sandboxes/projects/<project-id>/`. The ID is a SHA-256 hash of that path. The first `msbx shell` or `msbx run` starts the project's VM; concurrent sessions from the same project join that VM. The guest agent grows its session-worker pool as sessions arrive; concurrency has no configured session-count limit and is bounded by the VM's and Mac's available resources. When the last session exits, the manager waits three seconds for another session, then shuts the VM down.

Different initialized projects can run concurrently with separate guest disks and EFI state. Harnesses within one project share its guest VM, guest HOME, and writable project files. Each sandbox ID is bound to the canonical project path. Moving or renaming a project changes its ID and creates a separate sandbox.

To permanently delete the VM and guest data associated with the current project directory, stop its VM session and run:

```sh
/path/to/magical-sandboxes/bin/msbx delete
```

The command displays the full project path and requires interactive confirmation. It deletes the guest disk, guest HOME, sandbox-local sign-ins, and other guest files for this project. It leaves files in the target project directory untouched. Make sure all sessions have exited; the VM shuts down after a three-second idle grace period.

## Rebuild after code changes

### Check Swift formatting

Run the formatter in a disposable Docker container without installing `swift-format` on the host machine:

```sh
make format-swift
make lint-swift
```

`format-swift` updates Swift sources in place. `lint-swift` checks them without modifying files. Docker downloads the `swift:6.2` image on the first run if needed; both commands remove their containers when they exit.

### Only the Go host CLI changed

If only code under `cmd/msbx/` changed, rebuilding this program is sufficient:

```sh
go build -o bin/msbx ./cmd/msbx
```

### The guest agent or multiple host components changed

Run the full bootstrap again:

```sh
./scripts/bootstrap.sh
```

This rebuilds the host Go binaries, the Linux ARM64 guest agent, and the Swift VM helper, then signs the VM helper with the required entitlement. If guest agent source under `cmd/msbx-agent/` changed, bootstrap updates the `bin/msbx-agent-linux-arm64` binary. Rebuild the guest template to install it; existing per-project disks are not updated automatically.

### The Swift VM helper changed

The full bootstrap is recommended because it both compiles and signs the helper:

```sh
./scripts/bootstrap.sh
```

### Guest installation or wrapper code changed

If `scripts/prepare-cloud-guest.sh` changed, rebuild the clean template:

```sh
./scripts/bootstrap.sh
rm -rf .msbx-dev/sandboxes/template
./scripts/prepare-cloud-guest.sh
./scripts/provision.sh
```

**Warning:** `rm` deletes the clean template disk and its EFI state. Existing project VMs under `.msbx-dev/sandboxes/projects/` are not updated when the template is rebuilt; they keep their own disks, guest sign-ins, and files. Recreating a project VM deletes that VM's guest data.

To apply guest agent or installation changes to a project VM, rebuild the template above, then recreate that project's VM from its project directory:

```sh
/path/to/magical-sandboxes/bin/msbx delete
/path/to/magical-sandboxes/bin/msbx init
```

`msbx delete` permanently removes that project's guest disk, including its native harness sign-ins and settings. Sign in again after the VM is initialized. Repeat this for each project VM that should receive the updated guest image.

Documentation changes do not require a rebuild or reprovisioning.

## Credentials and state

Each project VM stores the harness sign-ins and settings in its guest HOME. These remain available after VM shutdown, are isolated from other projects, and are deleted by `msbx delete`. Guest VM disks are not backed up by msbx.

- Debian base image and cache: `.msbx-dev/guest/debian-13-generic-arm64-<DEBIAN_IMAGE_VERSION>/`
- Clean guest template: `.msbx-dev/sandboxes/template/`
- Per-project guest disk and EFI state: `.msbx-dev/sandboxes/projects/<project-id>/`
- Native harness login and settings: each project's guest HOME on its persistent VM disk

## Help and documentation

```sh
./bin/msbx help
./bin/msbx version
./bin/msbx doctor
```

- [System overview and VM behavior](docs/magical-sandboxes.md)
- [Codex startup and troubleshooting](docs/codex.md)
- [Claude Code startup and troubleshooting](docs/claude.md)
- [OpenCode startup and troubleshooting](docs/opencode.md)
