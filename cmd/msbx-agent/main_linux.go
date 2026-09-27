//go:build linux

package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	hostCID            = uint32(2)
	defaultPort        = uint32(4050)
	controlPort        = uint32(4052)
	initialWorkerCount = 1
	afVsock            = 40

	tiocgptn   = 0x80045430
	tiocsptlck = 0x40045431
	tiocswinsz = 0x5414
)

var projectMountMu sync.Mutex

type sockaddrVM struct {
	Family    uint16
	Reserved1 uint16
	Port      uint32
	CID       uint32
	Zero      [4]byte
}

type winSize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

func main() {
	port := defaultPort
	if len(os.Args) == 3 && os.Args[1] == "--port" {
		var p uint64
		if _, err := fmt.Sscanf(os.Args[2], "%d", &p); err != nil || p == 0 || p > 1<<32-1 {
			fmt.Fprintln(os.Stderr, "msbx-agent: invalid port")
			os.Exit(2)
		}
		port = uint32(p)
	} else if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: msbx-agent [--port N]")
		os.Exit(2)
	}

	go runControlWorker(controlPort)
	for worker := 0; worker < initialWorkerCount; worker++ {
		go runSessionWorker(port, worker)
	}
	select {}
}

func runSessionWorker(port uint32, worker int) {
	for {
		fd, err := connectHost(port)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		fmt.Fprintf(os.Stderr, "msbx-agent: session worker %d connected to host at %s\n", worker, time.Now().Format(time.RFC3339Nano))
		started := false
		err = handleSession(fd, func() {
			started = true
			go runSessionWorker(port, worker+1)
		})
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
			fmt.Fprintf(os.Stderr, "msbx-agent: session worker %d: %v\n", worker, err)
		}
		if started {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func runControlWorker(port uint32) {
	for {
		fd, err := connectHost(port)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		connection := os.NewFile(uintptr(fd), "msbx-control-vsock")
		if connection == nil {
			_ = syscall.Close(fd)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		command, err := readLine(connection, 128)
		_ = connection.Close()
		if err != nil {
			continue
		}
		if command == "STOP" {
			if err := exec.Command("/usr/bin/systemctl", "poweroff", "--no-block").Run(); err != nil {
				fmt.Fprintln(os.Stderr, "msbx-agent: unable to request guest poweroff:", err)
			}
			return
		}
	}
}

func connectHost(port uint32) (int, error) {
	fd, err := syscall.Socket(afVsock, syscall.SOCK_STREAM, 0)
	if err != nil {
		return -1, err
	}
	addr := sockaddrVM{Family: afVsock, Port: port, CID: hostCID}
	_, _, errno := syscall.Syscall(syscall.SYS_CONNECT, uintptr(fd), uintptr(unsafe.Pointer(&addr)), unsafe.Sizeof(addr))
	if errno != 0 {
		_ = syscall.Close(fd)
		return -1, errno
	}
	return fd, nil
}

func handleSession(fd int, onSessionStart func()) error {
	f := os.NewFile(uintptr(fd), "msbx-vsock")
	if f == nil {
		_ = syscall.Close(fd)
		return errors.New("unable to wrap vsock fd")
	}
	defer f.Close()

	magic, err := readLine(f, 64)
	if err != nil {
		return err
	}
	if err := validateShellProtocol(magic); err != nil {
		return err
	}

	cwdB64, err := readLine(f, 16*1024)
	if err != nil {
		return err
	}
	termB64, err := readLine(f, 1024)
	if err != nil {
		return err
	}
	rowsLine, err := readLine(f, 32)
	if err != nil {
		return err
	}
	colsLine, err := readLine(f, 32)
	if err != nil {
		return err
	}
	commandB64, err := readLine(f, 16*1024)
	if err != nil {
		return err
	}
	argsB64, err := readLine(f, 64*1024)
	if err != nil {
		return err
	}

	cwdBytes, err := base64.StdEncoding.DecodeString(cwdB64)
	if err != nil {
		return fmt.Errorf("invalid cwd: %w", err)
	}
	termBytes, err := base64.StdEncoding.DecodeString(termB64)
	if err != nil {
		return fmt.Errorf("invalid TERM: %w", err)
	}
	cwd := string(cwdBytes)
	term := string(termBytes)
	commandBytes, err := base64.StdEncoding.DecodeString(commandB64)
	if err != nil {
		return fmt.Errorf("invalid command: %w", err)
	}
	argsBytes, err := base64.StdEncoding.DecodeString(argsB64)
	if err != nil {
		return fmt.Errorf("invalid args: %w", err)
	}
	guestCommand := string(commandBytes)
	var guestArgs []string
	if len(argsBytes) > 0 {
		guestArgs = strings.Split(string(argsBytes), "\x1f")
	}
	if guestCommand == "" || !strings.HasPrefix(guestCommand, "/") {
		return errors.New("guest command must be absolute")
	}
	if cwd == "" || !strings.HasPrefix(cwd, "/") {
		return errors.New("cwd must be absolute")
	}
	if term == "" {
		term = "xterm-256color"
	}
	onSessionStart()

	var rows, cols uint16
	if _, err := fmt.Sscanf(rowsLine, "%d", &rows); err != nil || rows == 0 {
		rows = 24
	}
	if _, err := fmt.Sscanf(colsLine, "%d", &cols); err != nil || cols == 0 {
		cols = 80
	}

	fmt.Fprintf(os.Stderr, "msbx-agent: mount start at %s target=%s\n", time.Now().Format(time.RFC3339Nano), cwd)
	projectMountMu.Lock()
	err = mountProject(cwd)
	projectMountMu.Unlock()
	if err != nil {
		_, _ = io.WriteString(f, "ERROR "+err.Error()+"\n")
		return err
	}
	fmt.Fprintf(os.Stderr, "msbx-agent: mount complete at %s\n", time.Now().Format(time.RFC3339Nano))

	master, slave, err := openPTY(rows, cols)
	if err != nil {
		_, _ = io.WriteString(f, "ERROR pty: "+err.Error()+"\n")
		return err
	}
	defer master.Close()
	defer slave.Close()

	identity, err := sandboxIdentity("msbx")
	if err != nil {
		_, _ = io.WriteString(f, "ERROR sandbox identity: "+err.Error()+"\n")
		return err
	}

	cmd := exec.Command(guestCommand, guestArgs...)
	cmd.Dir = cwd
	cmd.Env = append(filteredBaseEnv(),
		"TERM="+term,
		"MSBX_SANDBOX=1",
		"HOME="+identity.home,
		"USER=msbx",
		"LOGNAME=msbx",
	)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0, Credential: identity.credential}

	fmt.Fprintf(os.Stderr, "msbx-agent: shell start at %s\n", time.Now().Format(time.RFC3339Nano))
	if err := cmd.Start(); err != nil {
		_, _ = io.WriteString(f, "ERROR shell: "+err.Error()+"\n")
		return err
	}
	_ = slave.Close()

	fmt.Fprintf(os.Stderr, "msbx-agent: sending READY at %s\n", time.Now().Format(time.RFC3339Nano))
	if _, err := io.WriteString(f, "READY\n"); err != nil {
		return err
	}

	frameWriter := &guestFrameWriter{dst: f}
	outputDone := make(chan error, 1)
	go func() {
		err := copyPTYFrames(frameWriter, master)
		outputDone <- err
	}()

	inputDone := make(chan error, 1)
	go func() { inputDone <- consumeFrames(f, master) }()

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-waitDone:
	case inputErr := <-inputDone:
		if inputErr != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
		}
		waitErr = <-waitDone
	}
	_ = master.Close()
	var outputErr error
	select {
	case outputErr = <-outputDone:
	case <-time.After(250 * time.Millisecond):
		fmt.Fprintln(os.Stderr, "msbx-agent: PTY output did not drain before timeout")
	}
	exitCode := guestExitCode(waitErr)
	if outputErr != nil {
		fmt.Fprintln(os.Stderr, "msbx-agent: unable to forward guest output:", outputErr)
		exitCode = 1
	}
	if err := frameWriter.write(frameExit, []byte{byte(exitCode)}); err != nil {
		return fmt.Errorf("send guest exit status: %w", err)
	}
	return waitErr
}

func copyPTYFrames(dst *guestFrameWriter, src io.Reader) error {
	buffer := make([]byte, 16*1024)
	for {
		n, readErr := src.Read(buffer)
		if n > 0 {
			if err := dst.write(frameOutput, buffer[:n]); err != nil {
				return err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, syscall.EIO) || errors.Is(readErr, os.ErrClosed) {
				return nil
			}
			return readErr
		}
	}
}

type sandboxUser struct {
	home       string
	credential *syscall.Credential
}

func sandboxIdentity(name string) (*sandboxUser, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("lookup %s user: %w", name, err)
	}
	uid64, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parse %s uid: %w", name, err)
	}
	gid64, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parse %s gid: %w", name, err)
	}
	groupIDs, err := u.GroupIds()
	if err != nil {
		return nil, fmt.Errorf("read %s groups: %w", name, err)
	}
	groups := make([]uint32, 0, len(groupIDs))
	for _, raw := range groupIDs {
		v, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("parse supplementary gid %q: %w", raw, err)
		}
		groups = append(groups, uint32(v))
	}
	return &sandboxUser{
		home:       u.HomeDir,
		credential: &syscall.Credential{Uid: uint32(uid64), Gid: uint32(gid64), Groups: groups},
	}, nil
}

func filteredBaseEnv() []string {
	blocked := map[string]bool{"HOME": true, "USER": true, "LOGNAME": true}
	out := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok && blocked[key] {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func openPTY(rows, cols uint16) (*os.File, *os.File, error) {
	fd, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	master := os.NewFile(uintptr(fd), "/dev/ptmx")
	if master == nil {
		_ = syscall.Close(fd)
		return nil, nil, errors.New("wrap ptmx")
	}

	unlock := int32(0)
	if errno := ioctl(fd, tiocsptlck, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		master.Close()
		return nil, nil, errno
	}
	var ptyNumber uint32
	if errno := ioctl(fd, tiocgptn, uintptr(unsafe.Pointer(&ptyNumber))); errno != 0 {
		master.Close()
		return nil, nil, errno
	}
	if err := setWinSize(fd, rows, cols); err != nil {
		master.Close()
		return nil, nil, err
	}

	slavePath := fmt.Sprintf("/dev/pts/%d", ptyNumber)
	slaveFD, err := syscall.Open(slavePath, syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	slave := os.NewFile(uintptr(slaveFD), slavePath)
	if slave == nil {
		_ = syscall.Close(slaveFD)
		master.Close()
		return nil, nil, errors.New("wrap pty slave")
	}
	return master, slave, nil
}

func setWinSize(fd int, rows, cols uint16) error {
	ws := winSize{Row: rows, Col: cols}
	if errno := ioctl(fd, tiocswinsz, uintptr(unsafe.Pointer(&ws))); errno != 0 {
		return errno
	}
	return nil
}

func ioctl(fd int, request uintptr, arg uintptr) syscall.Errno {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), request, arg)
	return errno
}

func consumeFrames(r io.Reader, master *os.File) error {
	header := make([]byte, 5)
	for {
		if _, err := io.ReadFull(r, header); err != nil {
			return err
		}
		frameType := header[0]
		n := binary.BigEndian.Uint32(header[1:5])
		if n > 1<<20 {
			return fmt.Errorf("frame too large: %d", n)
		}
		payload := make([]byte, int(n))
		if _, err := io.ReadFull(r, payload); err != nil {
			return err
		}

		switch frameType {
		case frameStdin:
			if len(payload) > 0 {
				if _, err := master.Write(payload); err != nil {
					return err
				}
			}
		case frameResize:
			if len(payload) != 4 {
				return errors.New("invalid resize frame")
			}
			rows := binary.BigEndian.Uint16(payload[0:2])
			cols := binary.BigEndian.Uint16(payload[2:4])
			if rows > 0 && cols > 0 {
				if err := setWinSize(int(master.Fd()), rows, cols); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unknown frame type: %d", frameType)
		}
	}
}

func mountProject(target string) error {
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}
	if exec.Command("mountpoint", "-q", target).Run() != nil {
		cmd := exec.Command("timeout", "10s", "mount", "-t", "virtiofs", "msbx-project", target)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("mount virtiofs: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}

	return nil
}

func readLine(r io.Reader, max int) (string, error) {
	buf := make([]byte, 0, 128)
	one := []byte{0}
	for len(buf) < max {
		n, err := r.Read(one)
		if n == 1 {
			if one[0] == '\n' {
				return strings.TrimSuffix(string(buf), "\r"), nil
			}
			buf = append(buf, one[0])
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("protocol line too long")
}
