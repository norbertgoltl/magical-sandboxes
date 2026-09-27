package doctor

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Check struct {
	Name     string
	OK       bool
	Detail   string
	Required bool
}

func Run(w io.Writer) int {
	fmt.Fprintln(w, "magical-sandboxes doctor")
	fmt.Fprintln(w)

	checks := []Check{
		checkDarwin(),
		checkAppleSilicon(),
		commandCheck("Xcode Command Line Tools", true, "xcode-select", "-p"),
		commandCheck("Swift", true, "swift", "--version"),
		checkVirtualizationFramework(),
		checkVirtualizationEntitlement(),
		commandCheck("Go", false, "go", "version"),
	}

	requiredFailure := false
	for _, c := range checks {
		marker := "✓"
		if !c.OK {
			if c.Required {
				marker = "✗"
				requiredFailure = true
			} else {
				marker = "!"
			}
		}
		fmt.Fprintf(w, "%s %-31s %s\n", marker, c.Name, c.Detail)
	}

	fmt.Fprintln(w)
	if requiredFailure {
		fmt.Fprintln(w, "Host is not ready for the next magical-sandboxes step.")
		return 1
	}

	if !hasCommand("go") {
		fmt.Fprintln(w, "Host virtualization prerequisites look ready.")
		fmt.Fprintln(w, "Go is not installed; it is only needed to build msbx from source.")
		fmt.Fprintln(w, "Run ./scripts/bootstrap.sh to see the recommended installation command.")
		return 0
	}

	fmt.Fprintln(w, "Host looks ready for magical-sandboxes development.")
	return 0
}

func checkDarwin() Check {
	ok := runtime.GOOS == "darwin"
	detail := runtime.GOOS
	if !ok {
		detail += " (macOS host required)"
	}
	return Check{Name: "macOS host", OK: ok, Detail: detail, Required: true}
}

func checkAppleSilicon() Check {
	ok := runtime.GOARCH == "arm64"
	detail := runtime.GOARCH
	if !ok {
		detail += " (Apple Silicon/arm64 required for v1)"
	}
	return Check{Name: "Apple Silicon", OK: ok, Detail: detail, Required: true}
}

func checkVirtualizationFramework() Check {
	if runtime.GOOS != "darwin" {
		return Check{Name: "Virtualization.framework", OK: false, Detail: "not on macOS", Required: true}
	}

	helper, err := locateVMHelper()
	if err != nil {
		return Check{Name: "Virtualization.framework", OK: false, Detail: err.Error(), Required: true}
	}

	cmd := exec.Command(helper, "probe")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		out := strings.TrimSpace(stdout.String())
		if out == "" {
			out = "available"
		}
		return Check{Name: "Virtualization.framework", OK: true, Detail: out, Required: true}
	}

	detail := firstLine(stderr.String())
	if detail == "" {
		detail = "native virtualization probe failed"
	}
	return Check{Name: "Virtualization.framework", OK: false, Detail: detail, Required: true}
}

func checkVirtualizationEntitlement() Check {
	if runtime.GOOS != "darwin" {
		return Check{Name: "VM helper entitlement", OK: false, Detail: "not on macOS", Required: true}
	}

	helper, err := locateVMHelper()
	if err != nil {
		return Check{Name: "VM helper entitlement", OK: false, Detail: err.Error(), Required: true}
	}

	cmd := exec.Command("codesign", "--display", "--entitlements", ":-", helper)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := firstLine(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return Check{Name: "VM helper entitlement", OK: false, Detail: detail, Required: true}
	}

	text := string(out)
	if !strings.Contains(text, "com.apple.security.virtualization") {
		return Check{Name: "VM helper entitlement", OK: false, Detail: "com.apple.security.virtualization missing; rerun ./scripts/bootstrap.sh", Required: true}
	}

	return Check{Name: "VM helper entitlement", OK: true, Detail: "com.apple.security.virtualization", Required: true}
}

func locateVMHelper() (string, error) {
	if override := os.Getenv("MSBX_VM_HELPER"); override != "" {
		if info, err := os.Stat(override); err == nil && !info.IsDir() {
			return override, nil
		}
		return "", fmt.Errorf("MSBX_VM_HELPER does not point to a file: %s", override)
	}

	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "msbx-vm")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	if path, lookErr := exec.LookPath("msbx-vm"); lookErr == nil {
		return path, nil
	}

	return "", fmt.Errorf("msbx-vm helper not found; run ./scripts/bootstrap.sh")
}

func commandCheck(name string, required bool, command string, args ...string) Check {
	path, err := exec.LookPath(command)
	if err != nil {
		return Check{Name: name, OK: false, Detail: command + " not found", Required: required}
	}
	cmd := exec.Command(path, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := firstLine(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return Check{Name: name, OK: false, Detail: detail, Required: required}
	}
	detail := firstLine(string(out))
	if detail == "" {
		detail = path
	}
	return Check{Name: name, OK: true, Detail: detail, Required: required}
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}
