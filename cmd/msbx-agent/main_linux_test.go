//go:build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFilteredBaseEnvRemovesIdentityVariables(t *testing.T) {
	t.Setenv("HOME", "/root")
	t.Setenv("USER", "root")
	t.Setenv("LOGNAME", "root")
	t.Setenv("MSBX_TEST_KEEP", "yes")

	env := filteredBaseEnv()
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, key := range []string{"HOME=", "USER=", "LOGNAME="} {
		if strings.Contains(joined, "\n"+key) {
			t.Fatalf("filtered env still contains %s", key)
		}
	}
	if !strings.Contains(joined, "\nMSBX_TEST_KEEP=yes\n") {
		t.Fatalf("filtered env dropped unrelated variable; process env size=%d", len(os.Environ()))
	}
}

func TestGuestExitCodeWithControllingPTYAndSignal(t *testing.T) {
	master, slave, err := openPTY(24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()

	cmd := exec.Command("/bin/sh", "-c", "kill -TERM $$")
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		slave.Close()
		t.Fatal(err)
	}
	_ = slave.Close()
	if got := guestExitCode(cmd.Wait()); got != 143 {
		t.Fatalf("guestExitCode(SIGTERM with PTY) = %d, want 143", got)
	}
}

func TestCopyPTYFramesTreatsClosedFileAsEndOfOutput(t *testing.T) {
	writer := &guestFrameWriter{dst: &bytes.Buffer{}}
	if err := copyPTYFrames(writer, errorReader{err: os.ErrClosed}); err != nil {
		t.Fatalf("copyPTYFrames(closed file) = %v, want nil", err)
	}
}

func TestCopyPTYFramesDrainsAfterProcessExitAndMasterClose(t *testing.T) {
	for i := 0; i < 20; i++ {
		master, slave, err := openPTY(24, 80)
		if err != nil {
			t.Fatal(err)
		}
		outputDone := make(chan error, 1)
		go func() { outputDone <- copyPTYFrames(&guestFrameWriter{dst: &bytes.Buffer{}}, master) }()

		cmd := exec.Command("/bin/true")
		cmd.Stdin = slave
		cmd.Stdout = slave
		cmd.Stderr = slave
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
		if err := cmd.Start(); err != nil {
			_ = slave.Close()
			_ = master.Close()
			t.Fatal(err)
		}
		_ = slave.Close()
		_ = cmd.Wait()
		_ = master.Close()

		select {
		case err := <-outputDone:
			if err != nil {
				t.Fatalf("iteration %d: copyPTYFrames() = %v, want nil", i, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("iteration %d: copyPTYFrames() did not stop after master close", i)
		}
	}
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }
