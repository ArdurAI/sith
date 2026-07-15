// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/localops"
	"github.com/ArdurAI/sith/internal/webui"
)

func TestDesktopHostServesTheExistingUIWithoutATCPListener(t *testing.T) {
	t.Parallel()
	host, err := newDesktopHost(t.Context(), &cacheReader{}, &fakeLocalClient{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Close)
	indexRequest := httptest.NewRequest(http.MethodGet, webui.DesktopOrigin+"/", nil)
	indexRequest.Host = "wails"
	index := httptest.NewRecorder()
	host.Handler().ServeHTTP(index, indexRequest)
	match := regexp.MustCompile(`name="sith-csrf-token" content="([^"]+)"`).FindStringSubmatch(index.Body.String())
	if index.Code != http.StatusOK || len(match) != 2 {
		t.Fatalf("desktop index = %d/%s", index.Code, index.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, webui.DesktopOrigin+"/api/v1/meta", nil)
	request.Host = "wails"
	request.Header.Set("X-Sith-CSRF", match[1])
	request.Header.Set("Origin", webui.DesktopOrigin)
	response := httptest.NewRecorder()
	host.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"telemetry":false`) {
		t.Fatalf("desktop response = %d/%s", response.Code, response.Body.String())
	}
}

func TestDesktopFolderImportUsesTheSharedSourceSeam(t *testing.T) {
	t.Parallel()
	host, err := newDesktopHost(t.Context(), &cacheReader{}, &fakeLocalClient{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Close)
	selected := t.TempDir() + "/team-kubeconfigs"
	called := ""
	host.newSource = func(directory string) (connector.Reader, localops.Client, error) {
		called = directory
		return &cacheReader{}, &fakeLocalClient{}, nil
	}
	if err := host.importDirectory(selected); err != nil {
		t.Fatal(err)
	}
	if called != selected {
		t.Fatalf("selected directory = %q, want %q", called, selected)
	}
}

func TestDesktopFolderImportRedactsFailure(t *testing.T) {
	t.Parallel()
	host, err := newDesktopHost(t.Context(), &cacheReader{}, &fakeLocalClient{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Close)
	selected := t.TempDir() + "/team-kubeconfigs"
	host.newSource = func(directory string) (connector.Reader, localops.Client, error) {
		return nil, nil, fmt.Errorf("unreadable %s", directory)
	}
	if err := host.importDirectory(selected); err == nil || strings.Contains(err.Error(), selected) {
		t.Fatalf("import failure = %v, want redacted error", err)
	}
}

func TestDesktopFolderImportKeepsTheActiveSessionWhenReplacementCannotStart(t *testing.T) {
	t.Parallel()
	host, err := newDesktopHost(t.Context(), &cacheReader{}, &fakeLocalClient{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Close)
	previous := host.session
	selected := t.TempDir() + "/team-kubeconfigs"
	host.newSource = func(string) (connector.Reader, localops.Client, error) {
		return &cacheReader{}, nil, nil
	}
	if err := host.importDirectory(selected); err == nil || strings.Contains(err.Error(), selected) {
		t.Fatalf("import failure = %v, want redacted replacement error", err)
	}
	if host.session != previous {
		t.Fatal("failed import replaced the active desktop session")
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, webui.DesktopOrigin+"/", nil)
	request.Host = "wails"
	host.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("active session status after failed import = %d", response.Code)
	}
}

func TestDesktopFolderImportCannotReviveAClosedHost(t *testing.T) {
	t.Parallel()
	host, err := newDesktopHost(t.Context(), &cacheReader{}, &fakeLocalClient{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Close)
	started := make(chan struct{})
	release := make(chan struct{})
	host.newSource = func(string) (connector.Reader, localops.Client, error) {
		close(started)
		<-release
		return &cacheReader{}, &fakeLocalClient{}, nil
	}
	result := make(chan error, 1)
	go func() { result <- host.importDirectory(t.TempDir() + "/team-kubeconfigs") }()
	select {
	case <-started:
	case <-t.Context().Done():
		t.Fatal("import did not reach source construction")
	}
	host.Close()
	close(release)
	if err := <-result; err == nil {
		t.Fatal("closed desktop host accepted a replacement session")
	}
	if host.session != nil {
		t.Fatal("closed desktop host retained a session")
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, webui.DesktopOrigin+"/", nil)
	request.Host = "wails"
	host.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed desktop handler status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestDesktopDirectorySourceRejectsUnsafeInputWithoutPathLeak(t *testing.T) {
	t.Parallel()
	unsafe := t.TempDir() + "/missing-kubeconfigs"
	if _, _, err := desktopDirectorySource(unsafe); err == nil || strings.Contains(err.Error(), unsafe) {
		t.Fatalf("desktopDirectorySource() error = %v, want safe rejection", err)
	}
}
