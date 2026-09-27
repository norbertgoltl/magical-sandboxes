package main

import (
	"archive/tar"
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/term"
)

const (
	backupFormatName        = "magical-sandboxes-vm-backup"
	backupFormatVersion     = 1
	backupEncryption        = "age-scrypt"
	backupCompression       = "zstd"
	backupDiskFormat        = "raw"
	backupVMBackend         = "apple-virtualization"
	backupGuestArchitecture = "arm64"
	backupManifestName      = "manifest.json"
	maxBackupManifestSize   = 1 << 20
	maxBackupMetadataSize   = 1 << 20
	maxBackupEFIFileSize    = 64 << 20
	maxBackupDiskFileSize   = 1 << 40
)

type backupFile struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type backupPayload struct {
	HostOS            string `json:"host_os"`
	HostArchitecture  string `json:"host_architecture"`
	Virtualization    string `json:"virtualization_backend"`
	GuestArchitecture string `json:"guest_architecture"`
	VirtualDiskFormat string `json:"virtual_disk_format"`
}

type backupManifest struct {
	FormatVersion int                    `json:"format_version"`
	Format        string                 `json:"format"`
	CreatedAt     time.Time              `json:"created_at"`
	Encryption    string                 `json:"encryption"`
	Compression   string                 `json:"compression"`
	Payload       backupPayload          `json:"payload"`
	Metadata      projectSandboxMetadata `json:"sandbox"`
	Disk          backupFile             `json:"disk"`
	EFI           backupFile             `json:"efi"`
	Sandbox       backupFile             `json:"sandbox_metadata"`
}

func runBackupCommand(args []string) int {
	if len(args) > 1 {
		fmt.Fprintln(os.Stderr, "usage: msbx backup [output-file]")
		return 2
	}
	if err := requireInteractiveTerminal("backup encryption"); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	_, projectPath, paths, err := currentProjectSandbox()
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	lifecycleUnlock, err := acquireProjectSandboxLock(paths.Dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	defer lifecycleUnlock()
	metadata, err := readProjectSandboxMetadata(paths, projectPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: project VM metadata is invalid: %v\n", err)
		return 1
	}
	for _, path := range []string{paths.Disk, paths.EFI} {
		if err := ensureRegularFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "msbx: project VM file is unavailable: %v\n", err)
			return 1
		}
	}
	diskUnlock, err := acquireLockFile(filepath.Join(paths.Dir, ".disk.raw.msbx.lock"), "this project's VM is running; stop its sessions before creating a backup")
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	defer diskUnlock()

	outputPath := ""
	if len(args) == 1 {
		outputPath, err = filepath.Abs(args[0])
	} else {
		outputPath, err = defaultBackupPath(projectPath, paths.ID)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to resolve backup path: %v\n", err)
		return 1
	}
	if err := ensureParentDirectory(outputPath, len(args) == 0); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	outputPath, err = resolveDestinationPath(outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to resolve backup destination: %v\n", err)
		return 1
	}
	if pathContains(paths.Dir, outputPath) {
		fmt.Fprintln(os.Stderr, "msbx: backup file must be stored outside this project's VM directory")
		return 1
	}
	if _, err := os.Lstat(outputPath); err == nil {
		fmt.Fprintf(os.Stderr, "msbx: backup file already exists: %s\n", outputPath)
		return 1
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "msbx: unable to inspect backup path: %v\n", err)
		return 1
	}
	passphrase, err := readBackupPassphrase(true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	stagingDirectory, err := os.MkdirTemp(filepath.Dir(outputPath), ".msbx-backup-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to create private backup staging directory: %v\n", err)
		return 1
	}
	defer os.RemoveAll(stagingDirectory)
	stagingPath := filepath.Join(stagingDirectory, filepath.Base(outputPath))
	fmt.Fprintf(os.Stderr, "Creating encrypted VM backup at %s.\n", outputPath)
	if err := createEncryptedBackup(stagingPath, passphrase, paths, metadata); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to create encrypted VM backup: %v\n", err)
		return 1
	}
	if err := publishBackupFile(stagingPath, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to publish backup without replacing an existing file: %v\n", err)
		return 1
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: backup created but cannot be inspected: %v\n", err)
		return 1
	}
	fmt.Printf("Encrypted VM backup created: %s (%s)\n", outputPath, formatByteSize(info.Size()))
	return 0
}

func runRestoreCommand(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: msbx restore <backup-file>")
		return 2
	}
	if err := requireInteractiveTerminal("backup decryption"); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	root, projectPath, paths, err := currentProjectSandbox()
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	backupPath, err := filepath.Abs(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to resolve backup file path: %v\n", err)
		return 1
	}
	if err := ensureRegularFile(backupPath); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: backup file is unavailable: %v\n", err)
		return 1
	}
	if _, err := os.Lstat(paths.Dir); err == nil {
		fmt.Fprintf(os.Stderr, "msbx: a sandbox directory already exists at %s; run msbx delete first if you intend to replace it\n", paths.Dir)
		return 1
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "msbx: unable to inspect project VM directory: %v\n", err)
		return 1
	}
	if err := ensureProjectSandboxParent(root, true); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	if err := ensurePrivateDirectory(filepath.Join(projectPath, ".msbx")); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to prepare project-local restore workspace: %v\n", err)
		return 1
	}
	stagingDirectory, err := os.MkdirTemp(filepath.Join(projectPath, ".msbx"), ".msbx-restore-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to create private restore workspace: %v\n", err)
		return 1
	}
	defer os.RemoveAll(stagingDirectory)

	passphrase, err := readBackupPassphrase(false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	manifest, payload, err := extractEncryptedBackup(backupPath, passphrase, stagingDirectory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: backup could not be decrypted or validated: %v\n", err)
		return 1
	}
	if err := validateBackupManifest(manifest, paths, projectPath); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: backup is not compatible with this project VM: %v\n", err)
		return 1
	}
	if err := validateBackupPayload(stagingDirectory, payload, manifest); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: backup contents failed validation: %v\n", err)
		return 1
	}

	fmt.Printf("Restore the encrypted VM backup for:\n  %s\nThis creates a new project VM and restores its harness sign-ins and guest data. Continue? [y/N] ", projectPath)
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && len(answer) == 0 {
		fmt.Fprintln(os.Stderr, "\nmsbx: unable to read confirmation")
		return 1
	}
	if !strings.EqualFold(strings.TrimSpace(answer), "y") && !strings.EqualFold(strings.TrimSpace(answer), "yes") {
		fmt.Println("Restore cancelled.")
		return 1
	}
	if err := os.Mkdir(paths.Dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to reserve project VM directory for restore: %v\n", err)
		return 1
	}
	lifecycleUnlock, err := acquireProjectSandboxLock(paths.Dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to lock project VM for restore: %v\n", err)
		// The directory may now belong to a concurrent initializer; remove it only if it is still empty.
		_ = os.Remove(paths.Dir)
		return 1
	}
	defer lifecycleUnlock()
	restoreSucceeded := false
	defer func() {
		if !restoreSucceeded {
			_ = os.RemoveAll(paths.Dir)
		}
	}()
	diskUnlock, err := acquireLockFile(filepath.Join(paths.Dir, ".disk.raw.msbx.lock"), "this project's VM disk is already in use")
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to lock project VM disk for restore: %v\n", err)
		return 1
	}
	defer diskUnlock()

	for _, name := range []string{"disk.raw", "efi-vars.fd", "sandbox.json"} {
		expected := payload[name]
		if err := copyRestoredFile(filepath.Join(stagingDirectory, name), filepath.Join(paths.Dir, name), expected); err != nil {
			fmt.Fprintf(os.Stderr, "msbx: unable to restore %s: %v\n", name, err)
			return 1
		}
	}
	if err := validateRestoredMetadata(paths, projectPath); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: restored VM metadata failed validation: %v\n", err)
		return 1
	}
	restoreSucceeded = true
	fmt.Printf("VM backup restored for %s\n", projectPath)
	return 0
}

func createEncryptedBackup(outputPath, passphrase string, paths projectSandboxPaths, metadata projectSandboxMetadata) error {
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = output.Close()
		}
	}()
	encryptedOutput, err := age.Encrypt(output, recipient)
	if err != nil {
		return err
	}
	encryptedClosed := false
	defer func() {
		if !encryptedClosed {
			_ = encryptedOutput.Close()
		}
	}()
	compressedOutput, err := zstd.NewWriter(encryptedOutput, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderCRC(true))
	if err != nil {
		return err
	}
	compressedClosed := false
	defer func() {
		if !compressedClosed {
			_ = compressedOutput.Close()
		}
	}()
	archive := tar.NewWriter(compressedOutput)
	archiveClosed := false
	defer func() {
		if !archiveClosed {
			_ = archive.Close()
		}
	}()

	manifest := backupManifest{
		FormatVersion: backupFormatVersion,
		Format:        backupFormatName,
		CreatedAt:     time.Now().UTC(),
		Encryption:    backupEncryption,
		Compression:   backupCompression,
		Payload:       currentBackupPayload(),
		Metadata:      metadata,
	}
	if manifest.Disk, err = writeArchiveFile(archive, "disk.raw", paths.Disk); err != nil {
		return err
	}
	if manifest.EFI, err = writeArchiveFile(archive, "efi-vars.fd", paths.EFI); err != nil {
		return err
	}
	if manifest.Sandbox, err = writeArchiveFile(archive, "sandbox.json", filepath.Join(paths.Dir, "sandbox.json")); err != nil {
		return err
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	manifestData = append(manifestData, '\n')
	if err := writeArchiveBytes(archive, backupManifestName, manifestData); err != nil {
		return err
	}
	if err := archive.Close(); err != nil {
		return err
	}
	archiveClosed = true
	if err := compressedOutput.Close(); err != nil {
		return err
	}
	compressedClosed = true
	if err := encryptedOutput.Close(); err != nil {
		return err
	}
	encryptedClosed = true
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	closed = true
	return nil
}

func writeArchiveFile(archive *tar.Writer, name, path string) (backupFile, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return backupFile{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return backupFile{}, fmt.Errorf("not a regular file: %s", path)
	}
	if info.Size() <= 0 || info.Size() > maxBackupDiskFileSize {
		return backupFile{}, fmt.Errorf("file size is outside supported backup limits: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return backupFile{}, err
	}
	defer file.Close()
	header := &tar.Header{
		Name:     name,
		Mode:     0o600,
		Size:     info.Size(),
		ModTime:  time.Unix(0, 0).UTC(),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatPAX,
	}
	if err := archive.WriteHeader(header); err != nil {
		return backupFile{}, err
	}
	checksum := sha256.New()
	written, err := io.CopyN(io.MultiWriter(archive, checksum), file, info.Size())
	if err != nil {
		return backupFile{}, err
	}
	if written != info.Size() {
		return backupFile{}, io.ErrUnexpectedEOF
	}
	var extra [1]byte
	if count, err := file.Read(extra[:]); count != 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return backupFile{}, fmt.Errorf("file changed while creating backup: %s", path)
	}
	currentInfo, err := file.Stat()
	if err != nil {
		return backupFile{}, err
	}
	if !os.SameFile(info, currentInfo) || currentInfo.Size() != info.Size() {
		return backupFile{}, fmt.Errorf("file changed while creating backup: %s", path)
	}
	return backupFile{Size: written, SHA256: hex.EncodeToString(checksum.Sum(nil))}, nil
}

func writeArchiveBytes(archive *tar.Writer, name string, data []byte) error {
	header := &tar.Header{
		Name:     name,
		Mode:     0o600,
		Size:     int64(len(data)),
		ModTime:  time.Unix(0, 0).UTC(),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatPAX,
	}
	if err := archive.WriteHeader(header); err != nil {
		return err
	}
	_, err := archive.Write(data)
	return err
}

func extractEncryptedBackup(backupPath, passphrase, stagingDirectory string) (backupManifest, map[string]backupFile, error) {
	input, err := os.Open(backupPath)
	if err != nil {
		return backupManifest{}, nil, err
	}
	defer input.Close()
	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return backupManifest{}, nil, err
	}
	identity.SetMaxWorkFactor(18)
	decrypted, err := age.Decrypt(input, identity)
	if err != nil {
		return backupManifest{}, nil, err
	}
	decompressed, err := zstd.NewReader(decrypted,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxMemory(256<<20),
		zstd.WithDecoderMaxWindow(128<<20),
	)
	if err != nil {
		return backupManifest{}, nil, err
	}
	decompressedClosed := false
	defer func() {
		if !decompressedClosed {
			decompressed.Close()
		}
	}()
	archive := tar.NewReader(decompressed)
	files := make(map[string]backupFile, 3)
	var manifestData []byte
	manifestSeen := false
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return backupManifest{}, nil, err
		}
		if manifestSeen {
			return backupManifest{}, nil, fmt.Errorf("backup contains entries after its manifest")
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return backupManifest{}, nil, fmt.Errorf("backup contains a non-regular archive entry")
		}
		switch header.Name {
		case "disk.raw":
			if _, exists := files[header.Name]; exists || header.Size <= 0 || header.Size > maxBackupDiskFileSize {
				return backupManifest{}, nil, fmt.Errorf("backup disk entry is duplicate or outside supported size limits")
			}
			files[header.Name], err = extractTarFile(archive, filepath.Join(stagingDirectory, header.Name), header.Size)
		case "efi-vars.fd":
			if _, exists := files[header.Name]; exists || header.Size <= 0 || header.Size > maxBackupEFIFileSize {
				return backupManifest{}, nil, fmt.Errorf("backup EFI entry is duplicate or outside supported size limits")
			}
			files[header.Name], err = extractTarFile(archive, filepath.Join(stagingDirectory, header.Name), header.Size)
		case "sandbox.json":
			if _, exists := files[header.Name]; exists || header.Size <= 0 || header.Size > maxBackupMetadataSize {
				return backupManifest{}, nil, fmt.Errorf("backup sandbox metadata entry is duplicate or outside supported size limits")
			}
			files[header.Name], err = extractTarFile(archive, filepath.Join(stagingDirectory, header.Name), header.Size)
		case backupManifestName:
			if header.Size <= 0 || header.Size > maxBackupManifestSize {
				return backupManifest{}, nil, fmt.Errorf("backup manifest is outside supported size limits")
			}
			manifestData, err = readTarBytes(archive, header.Size)
			manifestSeen = err == nil
		default:
			return backupManifest{}, nil, fmt.Errorf("backup contains an unexpected path: %s", header.Name)
		}
		if err != nil {
			return backupManifest{}, nil, err
		}
	}
	if !manifestSeen {
		return backupManifest{}, nil, fmt.Errorf("backup manifest is missing")
	}
	if len(files) != 3 {
		return backupManifest{}, nil, fmt.Errorf("backup is missing one or more required VM files")
	}
	if err := validateArchivePadding(decompressed); err != nil {
		return backupManifest{}, nil, err
	}
	decompressed.Close()
	decompressedClosed = true
	var manifest backupManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return backupManifest{}, nil, err
	}
	return manifest, files, nil
}

func extractTarFile(archive *tar.Reader, destination string, size int64) (backupFile, error) {
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return backupFile{}, err
	}
	if err := output.Truncate(size); err != nil {
		_ = output.Close()
		return backupFile{}, err
	}
	checksum := sha256.New()
	buffer := make([]byte, 128*1024)
	var position int64
	for position < size {
		length := int64(len(buffer))
		if size-position < length {
			length = size - position
		}
		count, err := io.ReadFull(archive, buffer[:length])
		if err != nil {
			_ = output.Close()
			return backupFile{}, err
		}
		chunk := buffer[:count]
		if _, err := checksum.Write(chunk); err != nil {
			_ = output.Close()
			return backupFile{}, err
		}
		if !allZero(chunk) {
			if _, err := output.WriteAt(chunk, position); err != nil {
				_ = output.Close()
				return backupFile{}, err
			}
		}
		position += int64(count)
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return backupFile{}, err
	}
	if err := output.Close(); err != nil {
		return backupFile{}, err
	}
	return backupFile{Size: size, SHA256: hex.EncodeToString(checksum.Sum(nil))}, nil
}

func readTarBytes(archive *tar.Reader, size int64) ([]byte, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(archive, data); err != nil {
		return nil, err
	}
	return data, nil
}

func validateArchivePadding(reader io.Reader) error {
	buffer := make([]byte, 64*1024)
	for {
		count, err := reader.Read(buffer)
		if count > 0 && !allZero(buffer[:count]) {
			return fmt.Errorf("backup contains unexpected data after the tar archive")
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func validateBackupManifest(manifest backupManifest, paths projectSandboxPaths, projectPath string) error {
	if manifest.FormatVersion != backupFormatVersion || manifest.Format != backupFormatName {
		return fmt.Errorf("unsupported backup format")
	}
	if manifest.Encryption != backupEncryption || manifest.Compression != backupCompression {
		return fmt.Errorf("unsupported backup encryption or compression")
	}
	if manifest.CreatedAt.IsZero() {
		return fmt.Errorf("backup creation time is missing")
	}
	if manifest.Metadata.Schema != projectSandboxSchema || manifest.Metadata.ID != paths.ID || manifest.Metadata.ProjectPath != projectPath {
		return fmt.Errorf("backup belongs to a different project path or sandbox ID")
	}
	if manifest.Payload.Virtualization != backupVMBackend || manifest.Payload.GuestArchitecture != backupGuestArchitecture || manifest.Payload.VirtualDiskFormat != backupDiskFormat {
		return fmt.Errorf("VM payload requires a different virtualization backend, guest architecture, or disk format")
	}
	if manifest.Payload.HostOS == "" || manifest.Payload.HostArchitecture == "" {
		return fmt.Errorf("backup host platform metadata is incomplete")
	}
	for name, file := range map[string]backupFile{
		"disk":             manifest.Disk,
		"EFI state":        manifest.EFI,
		"sandbox metadata": manifest.Sandbox,
	} {
		if file.Size <= 0 || len(file.SHA256) != sha256.Size*2 {
			return fmt.Errorf("backup %s metadata is invalid", name)
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return fmt.Errorf("backup %s checksum is invalid", name)
		}
	}
	if manifest.Disk.Size > maxBackupDiskFileSize || manifest.EFI.Size > maxBackupEFIFileSize || manifest.Sandbox.Size > maxBackupMetadataSize {
		return fmt.Errorf("backup contains a file larger than the supported size limit")
	}
	return nil
}

func validateBackupPayload(stagingDirectory string, payload map[string]backupFile, manifest backupManifest) error {
	for name, expected := range map[string]backupFile{
		"disk.raw":     manifest.Disk,
		"efi-vars.fd":  manifest.EFI,
		"sandbox.json": manifest.Sandbox,
	} {
		if payload[name] != expected {
			return fmt.Errorf("%s does not match its recorded size and checksum", name)
		}
	}
	metadataPath := filepath.Join(stagingDirectory, "sandbox.json")
	if err := ensureRegularFile(metadataPath); err != nil {
		return err
	}
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return err
	}
	var metadata projectSandboxMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return err
	}
	if metadata != manifest.Metadata {
		return fmt.Errorf("sandbox metadata does not match the manifest")
	}
	return nil
}

func readBackupPassphrase(confirm bool) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("backup encryption and decryption require an interactive terminal")
	}
	fmt.Fprint(os.Stderr, "Passphrase: ")
	passphrase, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(passphrase) || len(passphrase) < 12 {
		return "", fmt.Errorf("passphrase must contain at least 12 UTF-8 bytes")
	}
	if confirm {
		fmt.Fprint(os.Stderr, "Confirm passphrase: ")
		confirmation, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		if subtle.ConstantTimeCompare(passphrase, confirmation) != 1 {
			return "", fmt.Errorf("passphrases do not match")
		}
	}
	return string(passphrase), nil
}

func requireInteractiveTerminal(operation string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("%s requires an interactive terminal so the passphrase is not exposed in command arguments or piped input", operation)
	}
	return nil
}

func defaultBackupPath(projectPath, projectID string) (string, error) {
	backupDirectory := projectPath
	for _, component := range []string{".msbx", "backups", projectID} {
		backupDirectory = filepath.Join(backupDirectory, component)
		if err := ensurePrivateDirectory(backupDirectory); err != nil {
			return "", fmt.Errorf("unable to prepare default backup directory: %w", err)
		}
	}
	if err := os.Chmod(backupDirectory, 0o700); err != nil {
		return "", fmt.Errorf("unable to restrict default backup directory permissions: %w", err)
	}
	projectName := filepath.Base(projectPath)
	filename := fmt.Sprintf("%s-%s.msbxbackup.tar.zst.age", projectName, time.Now().UTC().Format("20060102T150405.000000000Z"))
	return filepath.Join(backupDirectory, filename), nil
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to use non-directory or symbolic-link path: %s", path)
	}
	return nil
}

func ensureParentDirectory(path string, create bool) error {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return fmt.Errorf("unable to create backup directory: %w", err)
		}
		info, err = os.Stat(parent)
	}
	if err != nil {
		return fmt.Errorf("backup directory does not exist or cannot be inspected: %s", parent)
	}
	if !info.IsDir() {
		return fmt.Errorf("backup destination parent is not a directory: %s", parent)
	}
	return nil
}

func resolveDestinationPath(path string) (string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func publishBackupFile(sourcePath, destinationPath string) error {
	if err := os.Link(sourcePath, destinationPath); err == nil {
		return nil
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, source)
	syncErr := destination.Sync()
	closeErr := destination.Close()
	if copyErr != nil {
		_ = os.Remove(destinationPath)
		return copyErr
	}
	if syncErr != nil {
		_ = os.Remove(destinationPath)
		return syncErr
	}
	if closeErr != nil {
		_ = os.Remove(destinationPath)
		return closeErr
	}
	return nil
}

func ensureRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("not a regular file: %s", path)
	}
	return nil
}

func copyRestoredFile(source, destination string, expected backupFile) error {
	if err := ensureRegularFile(source); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := copySparseContents(input, output, hash, expected.Size)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != expected.Size || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return fmt.Errorf("restored file does not match its recorded size and checksum")
	}
	return nil
}

func copySparseContents(source io.Reader, destination *os.File, checksum io.Writer, size int64) (int64, error) {
	if size < 0 {
		return 0, fmt.Errorf("invalid file size")
	}
	if err := destination.Truncate(size); err != nil {
		return 0, err
	}
	buffer := make([]byte, 128*1024)
	var position int64
	for position < size {
		length := int64(len(buffer))
		if size-position < length {
			length = size - position
		}
		count, err := io.ReadFull(source, buffer[:length])
		if err != nil {
			return position, err
		}
		chunk := buffer[:count]
		if checksum != nil {
			if _, err := checksum.Write(chunk); err != nil {
				return position, err
			}
		}
		if !allZero(chunk) {
			if _, err := destination.WriteAt(chunk, position); err != nil {
				return position, err
			}
		}
		position += int64(count)
	}
	var extra [1]byte
	if count, err := source.Read(extra[:]); count != 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return position, fmt.Errorf("file size does not match its manifest")
	}
	return position, nil
}

func validateRestoredMetadata(paths projectSandboxPaths, projectPath string) error {
	if _, err := readProjectSandboxMetadata(paths, projectPath); err != nil {
		return err
	}
	for _, path := range []string{paths.Disk, paths.EFI} {
		if err := ensureRegularFile(path); err != nil {
			return err
		}
	}
	return nil
}

func currentBackupPayload() backupPayload {
	return backupPayload{
		HostOS:            runtime.GOOS,
		HostArchitecture:  runtime.GOARCH,
		Virtualization:    backupVMBackend,
		GuestArchitecture: backupGuestArchitecture,
		VirtualDiskFormat: backupDiskFormat,
	}
}

func currentProjectSandbox() (string, string, projectSandboxPaths, error) {
	root, err := repoRoot()
	if err != nil {
		return "", "", projectSandboxPaths{}, err
	}
	projectPath, err := currentProjectPath(root)
	if err != nil {
		return "", "", projectSandboxPaths{}, err
	}
	return root, projectPath, projectSandboxForPath(root, projectPath), nil
}
