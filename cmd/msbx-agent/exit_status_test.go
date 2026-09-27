package main

import (
	"bytes"
	"errors"
	"os/exec"
	"testing"
)

func TestGuestExitCodeForSuccessfulProcess(t *testing.T) {
	if got := guestExitCode(nil); got != 0 {
		t.Fatalf("guestExitCode(nil) = %d, want 0", got)
	}
}

func TestGuestExitCodePreservesProcessExitCode(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 23")
	err := cmd.Run()
	if got := guestExitCode(err); got != 23 {
		t.Fatalf("guestExitCode(exit 23) = %d, want 23", got)
	}
}

func TestGuestExitCodePreservesExitCodeAboveSignalRange(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 143")
	if got := guestExitCode(cmd.Run()); got != 143 {
		t.Fatalf("guestExitCode(exit 143) = %d, want 143", got)
	}
}

func TestGuestExitCodeMapsSignalToShellConvention(t *testing.T) {
	cmd := exec.Command("sh", "-c", "kill -TERM $$")
	err := cmd.Run()
	if got := guestExitCode(err); got != 128+15 {
		t.Fatalf("guestExitCode(SIGTERM) = %d, want 143", got)
	}
}

func TestGuestExitCodeUsesFailureForUnknownWaitError(t *testing.T) {
	if got := guestExitCode(errors.New("wait failed")); got != 1 {
		t.Fatalf("guestExitCode(unknown error) = %d, want 1", got)
	}
}

func TestGuestFrameWriterEncodesExitStatus(t *testing.T) {
	var output bytes.Buffer
	writer := guestFrameWriter{dst: &shortWriter{dst: &output, maxWrite: 2}}
	if err := writer.write(frameExit, []byte{23}); err != nil {
		t.Fatalf("write exit frame: %v", err)
	}
	want := []byte{frameExit, 0, 0, 0, 1, 23}
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("encoded frame = %v, want %v", output.Bytes(), want)
	}
}

type shortWriter struct {
	dst      *bytes.Buffer
	maxWrite int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > w.maxWrite {
		p = p[:w.maxWrite]
	}
	return w.dst.Write(p)
}
