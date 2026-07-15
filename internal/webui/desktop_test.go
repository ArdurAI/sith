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

func TestInProcessHandlerWaitsForRequestsBeforeReplacement(t *testing.T) {
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
	replaced := make(chan struct{})
	go func() {
		handler.Replace(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		close(replaced)
	}()
	select {
	case <-replaced:
		t.Fatal("replacement completed while the previous request was in flight")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-served:
	case <-time.After(time.Second):
		t.Fatal("initial in-process request did not finish")
	}
	select {
	case <-replaced:
	case <-time.After(time.Second):
		t.Fatal("replacement did not finish after the prior request completed")
	}
}
