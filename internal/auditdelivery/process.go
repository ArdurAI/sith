// SPDX-License-Identifier: Apache-2.0

// Package auditdelivery provides the Hub's bounded, process-supervised local delivery path for
// already-sanitized authentication-refusal events.
package auditdelivery

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/ArdurAI/sith/internal/hubserver"
)

const (
	// ChildArgument starts the internal fixed-record audit sink. It is not a Cobra command and
	// bypasses normal configuration loading so the child inherits no deployment secrets.
	ChildArgument = "__sith-hub-auth-audit-sink"

	authRecordVersion byte = 1
	authRecordRefused byte = 1
)

var authRefusalLine = []byte(`{"level":"WARN","msg":"authentication refused","surface":"hub-auth","auth_outcome":"refused"}` + "\n")

// DropObserver observes a failed local send without receiving event contents or request metadata.
type DropObserver interface {
	ObserveAuthRefusalDeliveryDrop()
}

// Config fixes the trusted executable, inherited stderr, and low-cardinality drop observer for
// one process-supervised sink. The production default is the current Sith executable with only
// ChildArgument; Arguments exists solely to support isolated process lifecycle tests.
type Config struct {
	Executable string
	Arguments  []string
	Stderr     *os.File
	Drops      DropObserver
}

// ProcessObserver delivers the one closed authentication-refusal event through a nonblocking
// Unix datagram socket. It owns its child process and must be closed by the Hub runtime.
type ProcessObserver struct {
	mu       sync.Mutex
	parent   *os.File
	command  *exec.Cmd
	drops    DropObserver
	close    sync.Once
	closeErr error
}

var _ hubserver.AuthObserver = (*ProcessObserver)(nil)

// NewProcessObserver starts one restricted child with only an inherited Unix datagram descriptor
// and stderr. It never starts a goroutine, opens a listener, creates a socket pathname, or retains
// event data outside the kernel's bounded socket buffer.
func NewProcessObserver(config Config) (*ProcessObserver, error) {
	executable, err := trustedExecutable(config.Executable)
	if err != nil {
		return nil, err
	}
	arguments := config.Arguments
	if arguments == nil {
		arguments = []string{ChildArgument}
	}
	if len(arguments) == 0 {
		return nil, fmt.Errorf("construct process audit observer: child arguments are required")
	}
	stderr := config.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	if stderr == nil {
		return nil, fmt.Errorf("construct process audit observer: stderr is required")
	}

	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		return nil, fmt.Errorf("construct process audit observer: create datagram socket pair: %w", err)
	}
	unix.CloseOnExec(fds[0])
	unix.CloseOnExec(fds[1])
	parent := os.NewFile(uintptr(fds[0]), "sith-hub-auth-audit-parent")
	child := os.NewFile(uintptr(fds[1]), "sith-hub-auth-audit-child")
	if parent == nil || child == nil {
		if parent != nil {
			_ = parent.Close()
		} else {
			_ = unix.Close(fds[0])
		}
		if child != nil {
			_ = child.Close()
		} else {
			_ = unix.Close(fds[1])
		}
		return nil, fmt.Errorf("construct process audit observer: own datagram descriptors")
	}

	// #nosec G204 -- production resolves the current absolute Sith executable and supplies only the
	// fixed internal child argument; injectable values exist solely for the isolated lifecycle test.
	command := exec.Command(executable, arguments...)
	command.Env = []string{}
	command.Stderr = stderr
	command.ExtraFiles = []*os.File{child}
	if err := command.Start(); err != nil {
		_ = parent.Close()
		_ = child.Close()
		return nil, fmt.Errorf("construct process audit observer: start child: %w", err)
	}
	if err := child.Close(); err != nil {
		_ = parent.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, fmt.Errorf("construct process audit observer: close parent child descriptor: %w", err)
	}
	return &ProcessObserver{parent: parent, command: command, drops: config.Drops}, nil
}

func trustedExecutable(value string) (string, error) {
	if value == "" {
		var err error
		value, err = os.Executable()
		if err != nil {
			return "", fmt.Errorf("construct process audit observer: resolve current executable: %w", err)
		}
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("construct process audit observer: executable must be absolute")
	}
	info, err := os.Stat(value)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("construct process audit observer: executable is unavailable")
	}
	return value, nil
}

// ObserveAuth sends only the fixed wire form of a valid refusal. A full, dead, or closed socket
// records one unlabeled drop and returns immediately; it can never delay authentication.
func (observer *ProcessObserver) ObserveAuth(event hubserver.AuthEvent) {
	if observer == nil || event.Validate() != nil {
		return
	}
	payload := encodeAuthRefusal(event)
	if payload == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.parent == nil || sendDatagram(observer.parent, payload) != nil {
		if observer.drops != nil {
			observer.drops.ObserveAuthRefusalDeliveryDrop()
		}
	}
}

func encodeAuthRefusal(event hubserver.AuthEvent) []byte {
	if event.Validate() != nil || event.Outcome != hubserver.AuthOutcomeRefused {
		return nil
	}
	return []byte{authRecordVersion, authRecordRefused}
}

func sendDatagram(file *os.File, payload []byte) error {
	if file == nil || len(payload) != 2 {
		return fmt.Errorf("send audit datagram: payload is invalid")
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return fmt.Errorf("send audit datagram: access descriptor: %w", err)
	}
	if writeErr := raw.Write(func(fd uintptr) bool {
		err = unix.Send(int(fd), payload, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
		return true
	}); writeErr != nil {
		return fmt.Errorf("send audit datagram: access descriptor: %w", writeErr)
	}
	return err
}

// Close closes the parent socket, terminates the child, and reaps it. No copied stdio pipes or
// delivery goroutines exist, so shutdown cannot wait on a blocked local stderr write.
func (observer *ProcessObserver) Close() error {
	if observer == nil {
		return nil
	}
	observer.close.Do(func() {
		observer.mu.Lock()
		parent, command := observer.parent, observer.command
		observer.parent = nil
		observer.mu.Unlock()
		if parent != nil {
			if err := parent.Close(); err != nil && observer.closeErr == nil {
				observer.closeErr = fmt.Errorf("stop process audit observer: close parent datagram: %w", err)
			}
		}
		if command == nil || command.Process == nil {
			return
		}
		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) && observer.closeErr == nil {
			observer.closeErr = fmt.Errorf("stop process audit observer: terminate child: %w", err)
		}
		if err := command.Wait(); err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) && observer.closeErr == nil {
				observer.closeErr = fmt.Errorf("stop process audit observer: reap child: %w", err)
			}
		}
	})
	return observer.closeErr
}

// RunChild consumes and validates the fixed inherited datagrams, then writes a single structured
// local line. It intentionally has no configuration, environment, listener, persistence, or
// network path; a blocked stderr affects only this child process.
func RunChild(stderr *os.File) error {
	if stderr == nil {
		return fmt.Errorf("run process audit child: stderr is required")
	}
	socket := os.NewFile(uintptr(3), "sith-hub-auth-audit-child")
	if socket == nil {
		return fmt.Errorf("run process audit child: inherited descriptor is unavailable")
	}
	defer func() { _ = socket.Close() }()
	return runChild(socket, stderr)
}

func runChild(socket, stderr *os.File) error {
	if socket == nil || stderr == nil {
		return fmt.Errorf("run process audit child: socket and stderr are required")
	}
	fd := int(socket.Fd())
	buffer := make([]byte, 16)
	for {
		count, _, err := unix.Recvfrom(fd, buffer, 0)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.ECONNRESET) || errors.Is(err, unix.ENOTCONN) {
				return nil
			}
			return fmt.Errorf("run process audit child: receive datagram: %w", err)
		}
		if count == 0 {
			return nil
		}
		if !validAuthRefusalRecord(buffer[:count]) {
			continue
		}
		if _, err := stderr.Write(authRefusalLine); err != nil {
			return fmt.Errorf("run process audit child: write structured event: %w", err)
		}
	}
}

func validAuthRefusalRecord(record []byte) bool {
	return len(record) == 2 && record[0] == authRecordVersion && record[1] == authRecordRefused
}
