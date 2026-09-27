# Changelog

This file records user-facing project changes. Versions follow
[Semantic Versioning](https://semver.org/).

## [0.1.0] - 2026-09-26

Initial release of `msbx`, a macOS command-line tool for running a shell, Codex, Claude Code, and OpenCode inside Debian 13 ARM64 virtual machines on Apple Silicon. Each project has its own persistent VM disk, EFI state, guest HOME, native harness sign-ins, and writable VirtioFS project share. Multiple sessions in one project share its VM; separate projects can run their VMs concurrently. The VM manager shuts the guest down after the final session exits and a short idle grace period.

The CLI provides `init`, `status`, `shell`, `run`, and `delete` commands. The host VM helper uses Apple's Virtualization.framework, and the guest agent manages session PTYs and VM shutdown. Debian image, Debian package snapshot, and Docker package versions are pinned in `dependencies.lock`; harness installers use the upstream `latest` release.

Current limitations: the host target is macOS on Apple Silicon and the guest is Debian 13 ARM64. Guest-native sign-ins and settings are isolated per project and are not backed up by msbx. Session workers are created on demand; practical concurrency depends on available VM and host resources. Harness versions follow their upstream update policies. The guest VM control agent runs as root, while shell and harness processes run as the unprivileged `msbx` user.

Run `msbx init`, `msbx status`, `msbx shell`, `msbx run`, and `msbx delete` from the target project directory. Status reports VM state, sandbox metadata, disk sizes, guest template readiness, and manager log timestamp without starting the VM. Deletion requires interactive confirmation and removes the guest disk and guest-local data for that project.
