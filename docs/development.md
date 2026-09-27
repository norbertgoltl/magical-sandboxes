# Development guide

This guide records the project's development principles and long-term direction. It is intended to keep design decisions visible as the codebase grows.

## Platform portability

### Direction

The long-term goal is to make `magical-sandboxes` usable on more than one host platform. The current implementation is macOS-specific and must not be described as cross-platform. Support for another platform requires implementation and validation on that platform.

### Current implementation

- The host VM helper uses Apple's Virtualization framework and targets Apple silicon.
- VM disk preparation, provisioning, and lifecycle currently rely on macOS tooling.
- VM backups use a PAX TAR archive, Zstandard compression, and age passphrase encryption.
- The guest is Linux ARM64. Harnesses run inside the guest and use their native sign-in and configuration behavior.

These are current implementation details, not permanent architectural requirements. New work should avoid spreading host-platform assumptions into code that can be platform-independent.

### Design principles for new work

1. Keep core behavior independent of host operating system APIs where practical.
2. Put unavoidable operating system and virtualization differences behind small, explicit platform-specific components.
3. Separate VM lifecycle operations from backup-container operations so a platform-specific disk image tool is not required by the core backup model.
4. Do not claim that a backup is portable merely because its outer archive format is portable. A VM disk may still depend on its guest architecture, virtual hardware, disk format, or hypervisor.
5. Avoid host-global installation and persistent state during project development. Keep generated development state inside the repository or the target project workspace unless a change explicitly requires otherwise.
6. Preserve each harness's native login and settings behavior inside its project VM.

### Backup portability

The backup container format is platform-independent: PAX TAR stores the VM files and a versioned manifest, Zstandard compresses the archive, and age encrypts it with a passphrase. The implementation uses Go libraries rather than a host-provided disk-image utility. The passphrase is read interactively and never supplied as a command argument.

This does not make the VM payload universally portable. The current restore implementation requires the same Apple Virtualization backend, ARM64 guest, raw disk format, and canonical project path recorded in the manifest. A future backend must define its own payload compatibility or conversion before cross-platform restore can be claimed. The format and compatibility contract are recorded in [ADR 0001](decisions/0001-portable-backup-format.md).

### Platform support claims

Before calling the project cross-platform, define a host support matrix that includes the operating system, CPU architecture, virtualization backend, guest architecture, required tools, and backup/restore support. A successful compile alone does not establish platform support; the VM lifecycle and backup/restore flow must also be exercised on the listed platform.

## Recording design decisions

Use an Architecture Decision Record (ADR) when a design choice affects the platform boundary, on-disk formats, compatibility, security, or migration behavior. Store accepted decisions in `docs/decisions/` with a numbered English filename. Each ADR should record its status, context, decision, alternatives considered, and consequences.
