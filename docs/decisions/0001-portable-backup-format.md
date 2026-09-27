# ADR 0001: Portable encrypted VM backup format

- Status: Accepted
- Date: 2026-09-27

## Context

VM backups contain harness credentials and other private guest state. The backup format must protect that data, work without platform-specific disk-image tools, and make its compatibility requirements inspectable. The repository's current VM payload is macOS-specific even if its backup container is portable.

## Decision

Use a versioned PAX TAR archive compressed with Zstandard and encrypted with age's passphrase-based scrypt mode. The encrypted output uses the `.msbxbackup.tar.zst.age` suffix. The implementation uses Go libraries and prompts interactively for the passphrase; it does not accept the passphrase as a command-line argument or store it in project configuration.

The archive contains `disk.raw`, `efi-vars.fd`, `sandbox.json`, and `manifest.json`. The manifest is written last and records the format version, encryption and compression identifiers, source host, VM backend, guest architecture, disk format, project identity, file sizes, and SHA-256 checksums. Restore validates the archive and payload before creating VM state. Zero-filled disk ranges are represented as logical zero bytes in the archive and recreated as sparse ranges when extracted.

Without an explicit destination, backup files go under `.msbx/backups/<project-id>/` in the current project directory. Backup files and temporary staging files use owner-only permissions. Restore requires the original canonical project path and refuses to overwrite an existing project VM.

## Compatibility contract

The container format is platform-independent when a compatible implementation of PAX TAR, Zstandard, age scrypt, and this manifest version is available. The VM payload is currently limited to the Apple Virtualization backend, ARM64 guest, and raw disk format. Restore also requires the same canonical project path. A future platform or backend may read and verify the archive without being able to run the included VM payload; conversion or broader payload compatibility is a separate feature.

The passphrase cannot be recovered. Losing it makes the backup contents unrecoverable. Work-in-progress backups in the previous `.dmg` format are not supported by this implementation; no published format migration is promised.

## Alternatives considered

- **DMG disk images:** rejected because they depend on Apple's disk-image tooling and do not provide a platform-neutral container.
- **Unencrypted TAR or Zstandard files:** rejected because VM guest state includes credentials and private settings.
- **Host-provided encryption tools:** rejected because their availability and behavior would vary by host platform.

## Consequences

- The backup flow can be implemented using the existing Go toolchain without installing host tools.
- Backup files can be stored and transported between platforms that implement the selected formats, while restore remains subject to VM payload compatibility.
- The format version and manifest must be evolved deliberately. Readers should reject unknown format versions and unsupported encryption/compression identifiers.
- Backups can be large and creation scans the logical VM disk, including zero-filled ranges, to calculate integrity checksums.
- The current restore command is limited to the original project path and current VM backend; portability of the container must not be presented as portability of the guest VM.
