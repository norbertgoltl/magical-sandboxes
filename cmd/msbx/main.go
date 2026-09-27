package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/magical-sandboxes/magical-sandboxes/internal/doctor"
)

const version = "0.1.0"

func harnesses() map[string]string {
	return map[string]string{
		"codex":    "/usr/local/libexec/msbx-run-codex",
		"claude":   "/usr/local/libexec/msbx-run-claude",
		"opencode": "/usr/local/libexec/msbx-run-opencode",
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "doctor":
		os.Exit(doctor.Run(os.Stdout))
	case "status":
		os.Exit(runStatusCommand(os.Args[2:]))
	case "shell":
		os.Exit(runGuest("/bin/bash", []string{"-l"}))
	case "run":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: msbx run <harness> [args...]")
			os.Exit(2)
		}
		command, ok := harnesses()[os.Args[2]]
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown harness: %s\n", os.Args[2])
			os.Exit(2)
		}
		os.Exit(runGuest(command, os.Args[3:]))
	case "init":
		os.Exit(runInitCommand(os.Args[2:]))
	case "delete":
		os.Exit(runDeleteCommand(os.Args[2:]))
	case "version", "--version", "-v":
		fmt.Printf("msbx %s\n", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func repoRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("unable to locate executable: %w", err)
	}
	exe, _ = filepath.EvalSymlinks(exe)
	return filepath.Dir(filepath.Dir(exe)), nil
}

func validateSandboxProjectPath(projectPath, installRoot string) error {
	projectPath = filepath.Clean(projectPath)
	installRoot = filepath.Clean(installRoot)

	if pathContains(projectPath, installRoot) || pathContains(installRoot, projectPath) {
		return fmt.Errorf(
			"refusing to sandbox %s because it overlaps the msbx installation tree %s; run msbx from a separate project directory so the writable share cannot expose or modify the msbx installation",
			projectPath, installRoot,
		)
	}
	return nil
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func runGuest(guestCommand string, guestArgs []string) int {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}
	vm := filepath.Join(root, "bin", "msbx-vm")
	cwd, err := currentProjectPath(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}

	sandbox := projectSandboxForPath(root, cwd)
	for _, p := range []string{vm, sandbox.Disk, sandbox.EFI, filepath.Join(sandbox.Dir, "sandbox.json")} {
		if _, err := os.Stat(p); err != nil {
			if p == vm {
				fmt.Fprintf(os.Stderr, "msbx: missing %s\n", p)
				fmt.Fprintln(os.Stderr, "Run ./scripts/bootstrap.sh first.")
			} else {
				fmt.Fprintf(os.Stderr, "msbx: this project's sandbox is not initialized (%s)\n", p)
				fmt.Fprintln(os.Stderr, "From the target project directory, run: msbx init")
			}
			return 1
		}
	}
	resources, err := loadVMResources(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
		return 1
	}

	term := os.Getenv("TERM")
	if term == "" {
		term = "xterm-256color"
	}

	if guestCommand == "/bin/bash" {
		fmt.Fprintf(os.Stderr, "Starting sandbox shell for %s...\n", cwd)
	} else {
		fmt.Fprintf(os.Stderr, "Starting %s in sandbox for %s...\n", filepath.Base(guestCommand), cwd)
	}

	restore, err := makeTerminalRaw()
	if err != nil {
		fmt.Fprintf(os.Stderr, "msbx: unable to configure terminal: %v\n", err)
		return 1
	}
	defer restore()

	return runProjectSession(root, vm, sandbox, cwd, term, guestCommand, guestArgs, resources)
}

func makeTerminalRaw() (func(), error) {
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return func() {}, nil
	}
	get := exec.Command("stty", "-g")
	get.Stdin = os.Stdin
	out, err := get.Output()
	if err != nil {
		return nil, err
	}
	state := strings.TrimSpace(string(out))
	raw := exec.Command("stty", "raw", "-echo")
	raw.Stdin = os.Stdin
	if err := raw.Run(); err != nil {
		return nil, err
	}
	return func() {
		restore := exec.Command("stty", state)
		restore.Stdin = os.Stdin
		_ = restore.Run()
	}, nil
}

func usage() {
	io.WriteString(os.Stdout, `magical-sandboxes (msbx)

Usage:
  msbx doctor                     Check whether this Mac is ready
  msbx status                     Show this project's VM status
  msbx init                       Create or verify this project's isolated VM
  msbx delete                     Delete this project's isolated VM and guest data
  msbx shell                      Open a sandbox shell
  msbx run codex [args...]        Run Codex in this project's shared VM
  msbx run claude [args...]       Run Claude Code in this project's shared VM
  msbx run opencode [args...]     Run OpenCode in this project's shared VM
  msbx version                    Print the current version

Each project has one persistent VM. Concurrent shell and harness sessions from that
project share its guest HOME, native sign-in state, and writable project directory.
The last session stops the VM after a short idle grace period. The project share must
not overlap the msbx installation tree.
`)
}
