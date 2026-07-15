// SPDX-License-Identifier: Apache-2.0

package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type blockingLocalHandler struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (handler blockingLocalHandler) ServeHTTP(response http.ResponseWriter, _ *http.Request) {
	close(handler.started)
	<-handler.release
	response.WriteHeader(http.StatusNoContent)
}

func TestInProcessHandlerReplacesWithoutBlockingNewRequests(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	handler := NewInProcessHandler(blockingLocalHandler{started: started, release: release})
	served := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "wails://wails/", nil))
		close(served)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("initial in-process request did not start")
	}
	drained := handler.Replace(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	select {
	case <-drained:
		t.Fatal("previous session drained while its request was in flight")
	case <-time.After(20 * time.Millisecond):
	}
	fresh := httptest.NewRecorder()
	handler.ServeHTTP(fresh, httptest.NewRequest(http.MethodGet, "wails://wails/", nil))
	if fresh.Code != http.StatusNoContent {
		t.Fatalf("replacement handler status = %d, want %d", fresh.Code, http.StatusNoContent)
	}
	close(release)
	select {
	case <-served:
	case <-time.After(time.Second):
		t.Fatal("initial in-process request did not finish")
	}
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("previous session did not drain after its request completed")
	}
}
