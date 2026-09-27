package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateBackupManifestRequiresMatchingProjectAndChecksums(t *testing.T) {
	paths := projectSandboxPaths{ID: strings.Repeat("a", 64)}
	metadata := projectSandboxMetadata{
		Schema:      projectSandboxSchema,
		ID:          paths.ID,
		ProjectPath: "/tmp/project",
	}
	manifest := backupManifest{
		FormatVersion: backupFormatVersion,
		Format:        backupFormatName,
		CreatedAt:     time.Now(),
		Encryption:    backupEncryption,
		Compression:   backupCompression,
		Payload:       currentBackupPayload(),
		Metadata:      metadata,
		Disk:          backupFile{Size: 12, SHA256: strings.Repeat("b", 64)},
		EFI:           backupFile{Size: 12, SHA256: strings.Repeat("c", 64)},
		Sandbox:       backupFile{Size: 12, SHA256: strings.Repeat("d", 64)},
	}
	if err := validateBackupManifest(manifest, paths, "/tmp/project"); err != nil {
		t.Fatalf("valid backup manifest rejected: %v", err)
	}
	if err := validateBackupManifest(manifest, paths, "/tmp/other-project"); err == nil {
		t.Fatal("backup for another project path was accepted")
	}
	manifest.Disk.SHA256 = "invalid"
	if err := validateBackupManifest(manifest, paths, "/tmp/project"); err == nil {
		t.Fatal("backup manifest with invalid disk checksum was accepted")
	}
}

func TestDefaultBackupPathUsesProjectDirectory(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "work1")
	if err := os.Mkdir(projectPath, 0o700); err != nil {
		t.Fatal(err)
	}
	projectID := strings.Repeat("a", 64)
	path, err := defaultBackupPath(projectPath, projectID)
	if err != nil {
		t.Fatalf("default backup path: %v", err)
	}
	wantDirectory := filepath.Join(projectPath, ".msbx", "backups", projectID)
	if filepath.Dir(path) != wantDirectory {
		t.Fatalf("default backup path directory = %q, want %q", filepath.Dir(path), wantDirectory)
	}
	if !strings.HasPrefix(filepath.Base(path), "work1-") || !strings.HasSuffix(path, ".msbxbackup.tar.zst.age") {
		t.Fatalf("unexpected default backup filename: %q", filepath.Base(path))
	}
	info, err := os.Stat(wantDirectory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("backup directory permissions = %v, error = %v", info, err)
	}
}

func TestDefaultBackupPathRejectsSymlinkedMbsxDirectory(t *testing.T) {
	projectPath := t.TempDir()
	outsidePath := t.TempDir()
	if err := os.Symlink(outsidePath, filepath.Join(projectPath, ".msbx")); err != nil {
		t.Fatal(err)
	}
	if _, err := defaultBackupPath(projectPath, strings.Repeat("b", 64)); err == nil {
		t.Fatal("symlinked .msbx directory was accepted")
	}
	entries, err := os.ReadDir(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("default backup path wrote outside the project directory: %v", entries)
	}
}

func TestCopyRestoredFilePreservesSparseDiskContents(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "disk.raw")
	destination := filepath.Join(directory, "restored.raw")
	file, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(8*1024*1024 + 7); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("start"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("end"), 8*1024*1024); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(original)
	expected := backupFile{Size: int64(len(original)), SHA256: hex.EncodeToString(digest[:])}
	if err := copyRestoredFile(source, destination, expected); err != nil {
		t.Fatalf("copy sparse disk: %v", err)
	}
	restored, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, restored) {
		t.Fatal("restored sparse disk contents differ from source")
	}
}
