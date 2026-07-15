// SPDX-License-Identifier: Apache-2.0

package hubruntime

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/observability"
	"github.com/ArdurAI/sith/internal/pep"
)

func TestLoopbackMetricsServerServesOnlyFixedMetricsRouteAndStops(t *testing.T) {
	metrics, err := observability.New(observability.Config{Version: "v1.2.3", Commit: "0123456"})
	if err != nil {
		t.Fatal(err)
	}
	metrics.ObserveDecision(pep.VerbFleetRead, pep.DecisionOutcomeAllow, time.Millisecond)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + listener.Addr().String()
	server, err := newLoopbackMetricsServer(loopbackMetricsServerConfig{Listener: listener, Handler: metrics.Handler()})
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()

	for _, test := range []struct {
		method     string
		path       string
		statusCode int
		contains   string
	}{
		{method: http.MethodGet, path: loopbackMetricsPath, statusCode: http.StatusOK, contains: "sith_policy_decisions_total"},
		{method: http.MethodHead, path: loopbackMetricsPath, statusCode: http.StatusNotFound},
		{method: http.MethodPost, path: loopbackMetricsPath, statusCode: http.StatusNotFound},
		{method: http.MethodGet, path: loopbackMetricsPath + "?workspace=workspace-a", statusCode: http.StatusNotFound},
		{method: http.MethodGet, path: loopbackMetricsPath + "/", statusCode: http.StatusNotFound},
		{method: http.MethodGet, path: "/", statusCode: http.StatusNotFound},
		{method: http.MethodGet, path: "/v1/workspaces/workspace-a/fleet", statusCode: http.StatusNotFound},
	} {
		request, requestErr := http.NewRequest(test.method, endpoint+test.path, nil)
		if requestErr != nil {
			_ = server.Close()
			t.Fatal(requestErr)
		}
		response, requestErr := client.Do(request)
		if requestErr != nil {
			_ = server.Close()
			t.Fatalf("%s %s: %v", test.method, test.path, requestErr)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			_ = server.Close()
			t.Fatal(readErr)
		}
		if response.StatusCode != test.statusCode || (test.contains != "" && !strings.Contains(string(body), test.contains)) {
			_ = server.Close()
			t.Fatalf("%s %s status/body = %d/%q", test.method, test.path, response.StatusCode, body)
		}
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if connection, dialErr := net.DialTimeout("tcp", listener.Addr().String(), time.Second); dialErr == nil {
		_ = connection.Close()
		t.Fatal("loopback metrics listener remained reachable after Close")
	}
}

func TestNewLoopbackMetricsServerRejectsUnsafeConfiguration(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, test := range []struct {
		name   string
		config loopbackMetricsServerConfig
	}{
		{name: "missing listener", config: loopbackMetricsServerConfig{Handler: http.NotFoundHandler()}},
		{name: "missing handler", config: loopbackMetricsServerConfig{Listener: listener}},
		{name: "short shutdown timeout", config: loopbackMetricsServerConfig{Listener: listener, Handler: http.NotFoundHandler(), ShutdownTimeout: 500 * time.Millisecond}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if server, configErr := newLoopbackMetricsServer(test.config); configErr == nil {
				_ = server.Close()
				t.Fatal("newLoopbackMetricsServer accepted unsafe configuration")
			}
		})
	}
}
