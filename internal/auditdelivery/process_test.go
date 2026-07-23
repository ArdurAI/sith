// SPDX-License-Identifier: Apache-2.0

package auditdelivery

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ArdurAI/sith/internal/hubserver"
	"github.com/ArdurAI/sith/internal/tenancy"
)

func TestProcessObserverDeliversOnlyFixedRecordAndReapsChild(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	observer := newTestProcessObserver(t, writer, nil)

	observer.ObserveAuth(hubserver.AuthEvent{Outcome: hubserver.AuthOutcomeAccepted})
	observer.ObserveAuth(hubserver.AuthEvent{Outcome: "token=secret"})
	observer.ObserveAuth(hubserver.AuthEvent{Outcome: hubserver.AuthOutcomeRefused})
	line := make(chan string, 1)
	go func() {
		value, readErr := bufio.NewReader(reader).ReadString('\n')
		if readErr != nil {
			line <- "read error: " + readErr.Error()
			return
		}
		line <- value
	}()
	select {
	case value := <-line:
		if value != string(authRefusalLine) || strings.Contains(value, "secret") {
			t.Fatalf("child record = %q", value)
		}
	case <-time.After(time.Second):
		t.Fatal("child did not deliver fixed authentication refusal record")
	}
	closeObserverWithin(t, observer)
	if observer.command.ProcessState == nil {
		t.Fatal("child process was not reaped")
	}
}

func TestProcessObserverNeverDeliversOrDropsAcceptedAuthentication(t *testing.T) {
	var drops atomic.Uint64
	observer := &ProcessObserver{drops: dropObserverFunc(func() { drops.Add(1) })}
	observer.ObserveAuth(hubserver.AuthEvent{Outcome: hubserver.AuthOutcomeAccepted})
	if drops.Load() != 0 {
		t.Fatalf("accepted authentication delivery drops = %d, want 0", drops.Load())
	}
	observer.ObserveAuth(hubserver.AuthEvent{Outcome: hubserver.AuthOutcomeRefused})
	if drops.Load() != 1 {
		t.Fatalf("refused authentication delivery drops = %d, want 1", drops.Load())
	}
}

func TestProcessObserverDropsFullDatagramBufferWithoutBlocking(t *testing.T) {
	parent, child := socketPair(t)
	defer child.Close()
	var drops atomic.Uint64
	observer := &ProcessObserver{parent: parent, drops: dropObserverFunc(func() { drops.Add(1) })}
	started := time.Now()
	for range 50000 {
		observer.ObserveAuth(hubserver.AuthEvent{Outcome: hubserver.AuthOutcomeRefused})
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("full datagram buffer delayed authentication observer for %s", elapsed)
	}
	if drops.Load() == 0 {
		t.Fatal("full datagram buffer did not produce an observable drop")
	}
	closeObserverWithin(t, observer)
}

func TestBlockedChildCannotDelayAuthenticationRefusalOrEscapeShutdown(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	fillPipe(t, writer)
	observer := newTestProcessObserver(t, writer, nil)
	handler, err := hubserver.AuthenticateWithObserver(rejectingVerifier{}, observer, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("authentication refusal reached next handler")
	}))
	if err != nil {
		closeObserverWithin(t, observer)
		t.Fatal(err)
	}
	started := time.Now()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://hub.sith.test/v1/workspaces/workspace-a/fleet?token=secret", nil))
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		closeObserverWithin(t, observer)
		t.Fatalf("blocked child delayed authentication refusal for %s", elapsed)
	}
	if response.Code != http.StatusUnauthorized || response.Body.String() != "{\"error\":\"unauthorized\"}\n" {
		closeObserverWithin(t, observer)
		t.Fatalf("authentication refusal = %d/%q", response.Code, response.Body.String())
	}
	// The child receives the datagram, then blocks attempting its first stderr write because the
	// inherited pipe is already full. Parent shutdown must still kill and reap it promptly.
	time.Sleep(50 * time.Millisecond)
	closeObserverWithin(t, observer)
	if observer.command.ProcessState == nil {
		t.Fatal("blocked child process was not reaped")
	}
}

func TestAuthRefusalRecordRejectsMalformedRecords(t *testing.T) {
	for _, record := range [][]byte{
		nil,
		{},
		[]byte("token=secret"),
		{authRecordVersion},
		{authRecordVersion, authRecordRefused, 0},
		{0, authRecordRefused},
		{authRecordVersion, 0},
	} {
		if validAuthRefusalRecord(record) {
			t.Fatalf("validAuthRefusalRecord(%q) = true", record)
		}
	}
	if !validAuthRefusalRecord([]byte{authRecordVersion, authRecordRefused}) {
		t.Fatal("validAuthRefusalRecord() rejected the fixed record")
	}
}

// TestProcessAuditChildHelper runs only in the restricted subprocess started by
// newTestProcessObserver. It uses the same inherited-FD entrypoint as the shipped Sith binary.
func TestProcessAuditChildHelper(t *testing.T) {
	if !slicesContain(os.Args, "--audit-child") {
		return
	}
	if err := RunChild(os.Stderr); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func newTestProcessObserver(t *testing.T, stderr *os.File, drops DropObserver) *ProcessObserver {
	t.Helper()
	observer, err := NewProcessObserver(Config{
		Executable: os.Args[0],
		Arguments:  []string{"-test.run=^TestProcessAuditChildHelper$", "--", "--audit-child"},
		Stderr:     stderr,
		Drops:      drops,
	})
	if err != nil {
		t.Fatal(err)
	}
	return observer
}

func socketPair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.SetsockoptInt(fds[0], unix.SOL_SOCKET, unix.SO_SNDBUF, 256); err != nil {
		_ = unix.Close(fds[0])
		_ = unix.Close(fds[1])
		t.Fatal(err)
	}
	return os.NewFile(uintptr(fds[0]), "test-parent"), os.NewFile(uintptr(fds[1]), "test-child")
}

func fillPipe(t *testing.T, writer *os.File) {
	t.Helper()
	raw, err := writer.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Control(func(fd uintptr) {
		if setErr := unix.SetNonblock(int(fd), true); setErr != nil {
			err = setErr
			return
		}
		defer func() {
			if setErr := unix.SetNonblock(int(fd), false); setErr != nil && err == nil {
				err = setErr
			}
		}()
		buffer := make([]byte, 4096)
		for {
			_, writeErr := unix.Write(int(fd), buffer)
			if errors.Is(writeErr, unix.EAGAIN) || errors.Is(writeErr, unix.EWOULDBLOCK) {
				return
			}
			if writeErr != nil {
				err = writeErr
				return
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func closeObserverWithin(t *testing.T, observer *ProcessObserver) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- observer.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("process observer shutdown exceeded one second")
	}
}

type dropObserverFunc func()

func (function dropObserverFunc) ObserveAuthRefusalDeliveryDrop() { function() }

type rejectingVerifier struct{}

func (rejectingVerifier) Verify(context.Context, string) (tenancy.Principal, error) {
	return tenancy.Principal{}, errors.New("invalid token")
}

func slicesContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
