package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type lockState int

const (
	lockUnknown lockState = iota
	lockMissing
	lockFree
	lockHeld
)

func runStatusCommand(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: msbx status")
		return 2
	}
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	projectPath, err := currentProjectPath(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	return printProjectStatus(root, projectPath, os.Stdout)
}

func printProjectStatus(root, projectPath string, output *os.File) int {
	paths := projectSandboxForPath(root, projectPath)
	fmt.Fprintf(output, "Project: %s\n", projectPath)
	fmt.Fprintf(output, "Sandbox ID: %s\n", paths.ID)

	directoryInfo, err := os.Lstat(paths.Dir)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(output, "Sandbox: not initialized")
		fmt.Fprintln(output, "VM: stopped (no project sandbox)")
		printTemplateStatus(root, output)
		return 0
	}
	if err != nil {
		fmt.Fprintf(output, "Sandbox: unable to inspect (%v)\n", err)
		return 1
	}
	if !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		fmt.Fprintln(output, "Sandbox: invalid directory (refusing to inspect contents)")
		return 1
	}

	metadata, metadataErr := readProjectSandboxMetadata(paths, projectPath)
	if metadataErr != nil {
		fmt.Fprintf(output, "Sandbox: incomplete or invalid (%v)\n", metadataErr)
	} else {
		fmt.Fprintf(output, "Sandbox: initialized\n")
		fmt.Fprintf(output, "Created: %s\n", metadata.CreatedAt.Local().Format(time.RFC1123))
		fmt.Fprintf(output, "Template ID: %s\n", metadata.TemplateID)
	}
	printFileStatus("Guest disk", paths.Disk, output)
	printFileStatus("EFI state", paths.EFI, output)

	switch inspectFileLock(filepath.Join(paths.Dir, ".disk.raw.msbx.lock")) {
	case lockHeld:
		fmt.Fprintln(output, "VM: running (guest disk is locked)")
	case lockFree, lockMissing:
		managerLock := inspectFileLock(filepath.Join(paths.Dir, ".manager-start.lock"))
		if managerLock == lockHeld {
			fmt.Fprintln(output, "VM: starting (manager startup is in progress)")
		} else if managerLock == lockUnknown {
			fmt.Fprintln(output, "VM: unknown (unable to inspect manager lock)")
		} else {
			fmt.Fprintln(output, "VM: stopped")
		}
	case lockUnknown:
		fmt.Fprintln(output, "VM: unknown (unable to inspect guest disk lock)")
	}

	managerLog := filepath.Join(paths.Dir, "manager.log")
	if info, err := os.Stat(managerLog); err == nil && info.Mode().IsRegular() {
		fmt.Fprintf(output, "Manager log: %s (updated %s)\n", managerLog, info.ModTime().Local().Format(time.RFC1123))
	} else {
		fmt.Fprintln(output, "Manager log: not created")
	}
	printTemplateStatus(root, output)
	if metadataErr != nil {
		return 1
	}
	return 0
}

func readProjectSandboxMetadata(paths projectSandboxPaths, projectPath string) (projectSandboxMetadata, error) {
	metadataPath := filepath.Join(paths.Dir, "sandbox.json")
	info, err := os.Lstat(metadataPath)
	if err != nil {
		return projectSandboxMetadata{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return projectSandboxMetadata{}, fmt.Errorf("metadata is not a regular file")
	}
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return projectSandboxMetadata{}, err
	}
	var metadata projectSandboxMetadata
	if err := json.Unmarshal(data, &metadata); err != nil || metadata.Schema != projectSandboxSchema || metadata.ID != paths.ID || (metadata.ProjectPath != "" && metadata.ProjectPath != projectPath) {
		return projectSandboxMetadata{}, fmt.Errorf("metadata does not match this project")
	}
	return metadata, nil
}

func inspectFileLock(path string) lockState {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return lockMissing
	}
	if err != nil {
		return lockUnknown
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return lockHeld
		}
		return lockUnknown
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return lockFree
}

func printFileStatus(label, path string, output *os.File) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		fmt.Fprintf(output, "%s: missing\n", label)
		return
	}
	fmt.Fprintf(output, "%s: %s (%s)\n", label, path, formatByteSize(info.Size()))
}

func printTemplateStatus(root string, output *os.File) {
	templateDir := filepath.Join(root, ".msbx-dev", "sandboxes", "template")
	markerPath := filepath.Join(templateDir, "template.ready")
	markerInfo, err := os.Lstat(markerPath)
	if err != nil || !markerInfo.Mode().IsRegular() || markerInfo.Mode()&os.ModeSymlink != 0 {
		fmt.Fprintln(output, "Guest template: not ready")
		return
	}
	for _, name := range []string{"disk.raw", "efi-vars.fd"} {
		info, err := os.Lstat(filepath.Join(templateDir, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			fmt.Fprintf(output, "Guest template: incomplete (missing %s)\n", name)
			return
		}
	}
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		fmt.Fprintf(output, "Guest template: unable to read readiness marker (%v)\n", err)
		return
	}
	digest := sha256.Sum256(marker)
	fmt.Fprintf(output, "Guest template: ready (ID %s)\n", hex.EncodeToString(digest[:]))
}

func formatByteSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	for _, unit := range units {
		value /= 1024
		if value < 1024 || unit == units[len(units)-1] {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return fmt.Sprintf("%d B", size)
}
