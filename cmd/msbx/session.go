package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const projectSessionProtocol = "MSBX/8"

func runProjectSession(root, vm string, sandbox projectSandboxPaths, projectPath, term, guestCommand string, guestArgs []string, resources vmResources) int {
	rows, cols := terminalSize()
	arguments := strings.Join(guestArgs, "\x1f")
	header := []string{
		projectSessionProtocol,
		base64.StdEncoding.EncodeToString([]byte(projectPath)),
		base64.StdEncoding.EncodeToString([]byte(term)),
		strconv.Itoa(int(rows)),
		strconv.Itoa(int(cols)),
		base64.StdEncoding.EncodeToString([]byte(guestCommand)),
		base64.StdEncoding.EncodeToString([]byte(arguments)),
	}
	var connection *net.UnixConn
	startupDeadline := time.Now().Add(90 * time.Second)
	for {
		candidate, err := ensureProjectVM(root, vm, sandbox, projectPath, resources)
		if err != nil {
			fmt.Fprintf(os.Stderr, "msbx: %v\n", err)
			return 1
		}
		if err := writeSessionBytes(candidate, []byte(strings.Join(header, "\n")+"\n")); err != nil {
			candidate.Close()
			fmt.Fprintf(os.Stderr, "msbx: unable to send session request: %v\n", err)
			return 1
		}
		ready, err := readSessionLine(candidate, 8192)
		if err != nil {
			candidate.Close()
			fmt.Fprintf(os.Stderr, "msbx: project VM disconnected before starting the session: %v\n", err)
			return 1
		}
		if ready == "ERROR project VM is shutting down" && time.Now().Before(startupDeadline) {
			candidate.Close()
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if ready != "READY" {
			candidate.Close()
			fmt.Fprintf(os.Stderr, "msbx: %s\n", ready)
			return 1
		}
		connection = candidate
		break
	}
	defer connection.Close()

	writer := &sessionFrameWriter{connection: connection}
	go func() {
		buffer := make([]byte, 16*1024)
		for {
			n, readErr := os.Stdin.Read(buffer)
			if n > 0 && !writer.write(1, buffer[:n]) {
				return
			}
			if readErr != nil {
				_ = connection.CloseWrite()
				return
			}
		}
	}()

	resize := make(chan os.Signal, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	defer signal.Stop(resize)
	go func() {
		for range resize {
			rows, cols := terminalSize()
			payload := make([]byte, 4)
			binary.BigEndian.PutUint16(payload[0:2], rows)
			binary.BigEndian.PutUint16(payload[2:4], cols)
			if !writer.write(2, payload) {
				return
			}
		}
	}()

	for {
		frameType, payload, err := readSessionFrame(connection)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nmsbx: project VM session ended without an exit status: %v\n", err)
			return 1
		}
		switch frameType {
		case 3:
			if err := writeAll(os.Stdout, payload); err != nil {
				return 1
			}
		case 4:
			if len(payload) != 1 {
				fmt.Fprintln(os.Stderr, "msbx: guest returned an invalid exit status")
				return 1
			}
			return int(payload[0])
		default:
			fmt.Fprintf(os.Stderr, "msbx: invalid project session frame %d\n", frameType)
			return 1
		}
	}
}

func ensureProjectVM(root, vm string, sandbox projectSandboxPaths, projectPath string, resources vmResources) (*net.UnixConn, error) {
	identity := sha256.Sum256([]byte(root + "\x00" + sandbox.ID))
	socketPath := filepath.Join("/tmp", fmt.Sprintf("msbx-%d-%x.sock", os.Getuid(), identity[:12]))
	dial := func() (*net.UnixConn, error) {
		connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
		if err != nil {
			return nil, err
		}
		return connection, nil
	}
	if connection, err := dial(); err == nil {
		return connection, nil
	}

	startLockPath := filepath.Join(sandbox.Dir, ".manager-start.lock")
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if connection, err := dial(); err == nil {
			return connection, nil
		}
		unlock, err := acquireLockFile(startLockPath, "project VM startup is already in progress")
		if err != nil {
			time.Sleep(150 * time.Millisecond)
			continue
		}
		if connection, err := dial(); err == nil {
			unlock()
			return connection, nil
		}
		logFile, err := os.OpenFile(filepath.Join(sandbox.Dir, "manager.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			unlock()
			return nil, fmt.Errorf("unable to open project VM log: %w", err)
		}
		command := exec.Command(vm, "serve",
			"--disk", sandbox.Disk,
			"--efi-store", sandbox.EFI,
			"--share", projectPath,
			"--socket", socketPath,
			"--cpus", strconv.Itoa(resources.cpus),
			"--memory-mib", strconv.Itoa(resources.memoryMiB),
		)
		command.Stdin = nil
		command.Stdout = logFile
		command.Stderr = logFile
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := command.Start(); err != nil {
			_ = logFile.Close()
			unlock()
			return nil, fmt.Errorf("unable to start project VM manager: %w", err)
		}
		_ = command.Process.Release()
		_ = logFile.Close()
		startDeadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(startDeadline) {
			if connection, err := dial(); err == nil {
				unlock()
				return connection, nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		unlock()
		return nil, fmt.Errorf("project VM manager did not create its control socket; inspect %s", filepath.Join(sandbox.Dir, "manager.log"))
	}
	return nil, fmt.Errorf("timed out waiting for project VM manager startup")
}

func terminalSize() (uint16, uint16) {
	command := exec.Command("stty", "size")
	command.Stdin = os.Stdin
	output, err := command.Output()
	if err == nil {
		var rows, cols uint16
		if _, scanErr := fmt.Sscanf(strings.TrimSpace(string(output)), "%d %d", &rows, &cols); scanErr == nil && rows > 0 && cols > 0 {
			return rows, cols
		}
	}
	return 24, 80
}

func readSessionLine(connection net.Conn, max int) (string, error) {
	line := make([]byte, 0, 128)
	one := []byte{0}
	for len(line) < max {
		if _, err := connection.Read(one); err != nil {
			return "", err
		}
		if one[0] == '\n' {
			return strings.TrimSuffix(string(line), "\r"), nil
		}
		line = append(line, one[0])
	}
	return "", errors.New("session line is too long")
}

type sessionFrameWriter struct {
	connection net.Conn
	mu         sync.Mutex
}

func (writer *sessionFrameWriter) write(frameType byte, payload []byte) bool {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(payload) > 1<<20 {
		return false
	}
	header := make([]byte, 5)
	header[0] = frameType
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	return writeSessionBytes(writer.connection, header) == nil && writeSessionBytes(writer.connection, payload) == nil
}

func writeSessionBytes(destination io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := destination.Write(payload)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}

func readSessionFrame(connection net.Conn) (byte, []byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(connection, header); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length > 1<<20 {
		return 0, nil, fmt.Errorf("frame length %d exceeds limit", length)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(connection, payload); err != nil {
		return 0, nil, err
	}
	return header[0], payload, nil
}

func writeAll(destination *os.File, payload []byte) error {
	for len(payload) > 0 {
		n, err := destination.Write(payload)
		if err != nil {
			return err
		}
		payload = payload[n:]
	}
	return nil
}
