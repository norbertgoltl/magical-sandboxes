package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const projectSandboxSchema = 1

type projectSandboxPaths struct {
	ID   string
	Dir  string
	Disk string
	EFI  string
}

type projectSandboxMetadata struct {
	Schema      int       `json:"schema"`
	ID          string    `json:"id"`
	ProjectPath string    `json:"project_path,omitempty"`
	TemplateID  string    `json:"template_id"`
	CreatedAt   time.Time `json:"created_at"`
}

func projectSandboxForPath(root, projectPath string) projectSandboxPaths {
	digest := sha256.Sum256([]byte(filepath.Clean(projectPath)))
	id := hex.EncodeToString(digest[:])
	dir := filepath.Join(root, ".msbx-dev", "sandboxes", "projects", id)
	return projectSandboxPaths{
		ID:   id,
		Dir:  dir,
		Disk: filepath.Join(dir, "disk.raw"),
		EFI:  filepath.Join(dir, "efi-vars.fd"),
	}
}

func initializeProjectSandbox(root, projectPath string) (projectSandboxPaths, bool, error) {
	paths := projectSandboxForPath(root, projectPath)
	if err := ensureProjectSandboxParent(root, true); err != nil {
		return paths, false, err
	}
	templateDir := filepath.Join(root, ".msbx-dev", "sandboxes", "template")
	templateDisk := filepath.Join(templateDir, "disk.raw")
	templateEFI := filepath.Join(templateDir, "efi-vars.fd")
	templateMarker := filepath.Join(templateDir, "template.ready")

	if err := os.Mkdir(paths.Dir, 0o700); err != nil && !os.IsExist(err) {
		return paths, false, fmt.Errorf("unable to create project sandbox directory: %w", err)
	}
	projectDirInfo, err := os.Lstat(paths.Dir)
	if err != nil || !projectDirInfo.IsDir() || projectDirInfo.Mode()&os.ModeSymlink != 0 {
		return paths, false, fmt.Errorf("refusing to use invalid project sandbox directory: %s", paths.Dir)
	}
	lock, err := acquireProjectSandboxLock(paths.Dir)
	if err != nil {
		return paths, false, err
	}
	defer lock()

	metadataPath := filepath.Join(paths.Dir, "sandbox.json")
	if info, statErr := os.Lstat(metadataPath); statErr == nil {
		if !info.Mode().IsRegular() {
			return paths, false, fmt.Errorf("project sandbox metadata is not a regular file: %s", metadataPath)
		}
		data, readErr := os.ReadFile(metadataPath)
		if readErr != nil {
			return paths, false, fmt.Errorf("unable to read project sandbox metadata: %w", readErr)
		}
		var metadata projectSandboxMetadata
		if json.Unmarshal(data, &metadata) != nil || metadata.Schema != projectSandboxSchema || metadata.ID != paths.ID {
			return paths, false, fmt.Errorf("project sandbox metadata is invalid: %s", metadataPath)
		}
		if metadata.ProjectPath != "" && metadata.ProjectPath != projectPath {
			return paths, false, fmt.Errorf("project sandbox path does not match the current working directory: %s", metadata.ProjectPath)
		}
		for _, p := range []string{paths.Disk, paths.EFI} {
			if info, statErr := os.Lstat(p); statErr != nil || !info.Mode().IsRegular() {
				return paths, false, fmt.Errorf("initialized project sandbox is incomplete: %s", p)
			}
		}
		if metadata.ProjectPath == "" {
			metadata.ProjectPath = projectPath
			if err := writeProjectSandboxMetadata(metadataPath, paths.Dir, metadata); err != nil {
				return paths, false, err
			}
		}
		return paths, false, nil
	} else if !os.IsNotExist(statErr) {
		return paths, false, fmt.Errorf("unable to inspect project sandbox metadata: %w", statErr)
	}

	for _, p := range []string{paths.Disk, paths.EFI} {
		if _, statErr := os.Lstat(p); statErr == nil {
			return paths, false, fmt.Errorf("refusing to overwrite existing, unrecognized sandbox file: %s", p)
		} else if !os.IsNotExist(statErr) {
			return paths, false, fmt.Errorf("unable to inspect sandbox file %s: %w", p, statErr)
		}
	}

	markerInfo, err := os.Lstat(templateMarker)
	if err != nil || !markerInfo.Mode().IsRegular() {
		return paths, false, fmt.Errorf("guest template is not ready; run ./scripts/prepare-cloud-guest.sh and ./scripts/provision.sh first")
	}
	marker, err := os.ReadFile(templateMarker)
	if err != nil {
		return paths, false, fmt.Errorf("guest template is not ready; run ./scripts/prepare-cloud-guest.sh and ./scripts/provision.sh first")
	}
	for _, p := range []string{templateDisk, templateEFI} {
		if info, statErr := os.Lstat(p); statErr != nil || !info.Mode().IsRegular() {
			return paths, false, fmt.Errorf("guest template is incomplete: missing %s; rebuild it with ./scripts/prepare-cloud-guest.sh and ./scripts/provision.sh", p)
		}
	}

	templateLock, err := acquireLockFile(
		filepath.Join(templateDir, ".disk.raw.msbx.lock"),
		"guest template is currently being provisioned",
	)
	if err != nil {
		return paths, false, err
	}
	defer templateLock()

	created := make([]string, 0, 2)
	cleanup := func() {
		for _, p := range created {
			_ = os.Remove(p)
		}
	}
	for _, pair := range [][2]string{{templateDisk, paths.Disk}, {templateEFI, paths.EFI}} {
		if err := cloneFile(pair[0], pair[1]); err != nil {
			cleanup()
			return paths, false, err
		}
		created = append(created, pair[1])
	}

	templateHash := sha256.Sum256(marker)
	metadata := projectSandboxMetadata{
		Schema:      projectSandboxSchema,
		ID:          paths.ID,
		ProjectPath: projectPath,
		TemplateID:  hex.EncodeToString(templateHash[:]),
		CreatedAt:   time.Now().UTC(),
	}
	if err := writeProjectSandboxMetadata(metadataPath, paths.Dir, metadata); err != nil {
		cleanup()
		return paths, false, err
	}
	return paths, true, nil
}

func writeProjectSandboxMetadata(metadataPath, directory string, metadata projectSandboxMetadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("unable to encode project sandbox metadata: %w", err)
	}
	data = append(data, '\n')
	tmp := filepath.Join(directory, ".sandbox.json.tmp")
	tmpFile, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("unable to create project sandbox metadata: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("unable to write project sandbox metadata: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("unable to sync project sandbox metadata: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("unable to close project sandbox metadata: %w", err)
	}
	if err := os.Rename(tmp, metadataPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("unable to finalize project sandbox metadata: %w", err)
	}
	return nil
}

func cloneFile(source, destination string) error {
	clone := exec.Command("cp", "-c", source, destination)
	if output, err := clone.CombinedOutput(); err == nil {
		return nil
	} else {
		_ = os.Remove(destination)
		copy := exec.Command("cp", "-p", source, destination)
		if copyOutput, copyErr := copy.CombinedOutput(); copyErr != nil {
			_ = os.Remove(destination)
			return fmt.Errorf("unable to clone template file %s: APFS clone failed (%s); copy failed: %s: %w", source, string(output), string(copyOutput), copyErr)
		}
	}
	return nil
}

func acquireProjectSandboxLock(directory string) (func(), error) {
	return acquireLockFile(filepath.Join(directory, ".lifecycle.lock"), "this project's sandbox is already being initialized")
}

func ensureProjectSandboxParent(root string, create bool) error {
	parent := root
	for _, component := range []string{".msbx-dev", "sandboxes", "projects"} {
		parent = filepath.Join(parent, component)
		info, err := os.Lstat(parent)
		if os.IsNotExist(err) && create {
			if err := os.Mkdir(parent, 0o700); err != nil && !os.IsExist(err) {
				return fmt.Errorf("unable to create sandbox directory %s: %w", parent, err)
			}
			info, err = os.Lstat(parent)
		}
		if err != nil {
			return fmt.Errorf("unable to inspect sandbox directory %s: %w", parent, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to use non-directory or symbolic-link sandbox path: %s", parent)
		}
	}
	return nil
}

func acquireLockFile(path, lockedMessage string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("unable to open project sandbox lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, fmt.Errorf("%s", lockedMessage)
		}
		return nil, fmt.Errorf("unable to lock project sandbox: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func runInitCommand(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: msbx init")
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
	paths, created, err := initializeProjectSandbox(root, projectPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	if created {
		fmt.Printf("Initialized project sandbox %s for %s\n", paths.ID, projectPath)
	} else {
		fmt.Printf("Project sandbox %s is already initialized for %s\n", paths.ID, projectPath)
	}
	return 0
}

func runDeleteCommand(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: msbx delete")
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
	paths := projectSandboxForPath(root, projectPath)
	if err := ensureProjectSandboxParent(root, false); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	dirInfo, err := os.Lstat(paths.Dir)
	if os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "msbx: no VM sandbox exists for %s\n", projectPath)
		return 1
	}
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		fmt.Fprintf(os.Stderr, "msbx: refusing to delete an invalid sandbox path: %s\n", paths.Dir)
		return 1
	}

	lifecycleUnlock, err := acquireProjectSandboxLock(paths.Dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	defer lifecycleUnlock()
	metadataPath := filepath.Join(paths.Dir, "sandbox.json")
	metadataInfo, err := os.Lstat(metadataPath)
	if err != nil || !metadataInfo.Mode().IsRegular() {
		fmt.Fprintf(os.Stderr, "msbx: refusing to delete a sandbox without valid metadata: %s\n", metadataPath)
		return 1
	}
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to read sandbox metadata: %v\n", err)
		return 1
	}
	var metadata projectSandboxMetadata
	if json.Unmarshal(metadataBytes, &metadata) != nil || metadata.Schema != projectSandboxSchema || metadata.ID != paths.ID || (metadata.ProjectPath != "" && metadata.ProjectPath != projectPath) {
		fmt.Fprintln(os.Stderr, "msbx: sandbox metadata does not match the current working directory; refusing deletion")
		return 1
	}
	for _, p := range []string{paths.Disk, paths.EFI} {
		info, statErr := os.Lstat(p)
		if statErr != nil || !info.Mode().IsRegular() {
			fmt.Fprintf(os.Stderr, "msbx: refusing to delete an incomplete sandbox: %s\n", p)
			return 1
		}
	}
	diskUnlock, err := acquireLockFile(filepath.Join(paths.Dir, ".disk.raw.msbx.lock"), "this project's VM is running; stop it before deleting the sandbox")
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	defer diskUnlock()

	if info, err := os.Stdin.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		fmt.Fprintln(os.Stderr, "msbx: deletion requires an interactive terminal")
		return 1
	}
	fmt.Printf("This permanently deletes the VM, guest HOME, sign-ins, and other guest data for:\n  %s\nContinue? [y/N] ", projectPath)
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && len(answer) == 0 {
		fmt.Fprintln(os.Stderr, "\nmsbx: unable to read confirmation")
		return 1
	}
	if !strings.EqualFold(strings.TrimSpace(answer), "y") && !strings.EqualFold(strings.TrimSpace(answer), "yes") {
		fmt.Println("Deletion cancelled.")
		return 1
	}

	deletedDir := paths.Dir + ".deleting-" + fmt.Sprintf("%d", time.Now().UnixNano())
	if err := os.Rename(paths.Dir, deletedDir); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to detach project VM directory: %v\n", err)
		return 1
	}
	if err := os.RemoveAll(deletedDir); err != nil {
		fmt.Fprintf(os.Stderr, "msbx: sandbox is detached but cleanup failed; remove %s manually: %v\n", deletedDir, err)
		return 1
	}
	fmt.Printf("Deleted project VM for %s\n", projectPath)
	return 0
}

func currentProjectPath(installRoot string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("unable to read current directory: %w", err)
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("unable to resolve current directory: %w", err)
	}
	if err := validateSandboxProjectPath(cwd, installRoot); err != nil {
		return "", err
	}
	return cwd, nil
}
