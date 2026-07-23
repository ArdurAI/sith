// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

func TestTablePrinterRequestsAndDecodesServerColumns(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/namespaces/apps/configmaps" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Accept") != tableAccept {
			http.Error(writer, "missing table accept", http.StatusNotAcceptable)
			return
		}
		if request.URL.Query().Get("labelSelector") != "app=sample" {
			http.Error(writer, "missing selector", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(metav1.Table{
			TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "Table"},
			ColumnDefinitions: []metav1.TableColumnDefinition{
				{Name: "Name", Type: "string"},
				{Name: "Data", Type: "integer"},
				{Name: "Age", Type: "string", Priority: 1},
			},
			Rows: []metav1.TableRow{{
				Cells:  []any{"settings", int64(2), "5m"},
				Object: runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"settings","namespace":"apps"}}`)},
			}},
		})
	}))
	defer server.Close()

	printer, err := newTablePrinter(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatalf("newTablePrinter() error = %v", err)
	}
	fields, err := printer(context.Background(), resourceSpec{
		kind: "ConfigMap", gvr: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, namespaced: true,
	}, tableRequest{
		namespace: "apps", labelSelector: "app=sample", rowBudget: 1,
		retainKeys: map[string]struct{}{tableObjectKey("apps", "settings"): {}},
	})
	if err != nil {
		t.Fatalf("printer() error = %v", err)
	}
	got := fields[tableObjectKey("apps", "settings")]
	if len(got) != 3 || got[1].Value != "2" || got[2].Priority != 1 ||
		!slices.Equal([]string{got[0].Name, got[1].Name, got[2].Name}, []string{"Name", "Data", "Age"}) {
		t.Fatalf("fields = %#v", got)
	}
}

func TestTablePrinterPaginatesOpaqueTokensAndRetainsSelectedRows(t *testing.T) {
	t.Parallel()
	const opaqueToken = "opaque+/=token"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		writer.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			if request.URL.Query().Get("limit") != "3" || request.URL.Query().Get("continue") != "" {
				t.Errorf("first query = %q, want limit=3 without continuation", request.URL.RawQuery)
			}
			writeTable(t, writer, opaqueToken,
				tableRow("apps", "first", "1"),
				tableRow("apps", "ignored", "2"),
			)
		case 2:
			if request.URL.Query().Get("limit") != "1" || request.URL.Query().Get("continue") != opaqueToken {
				t.Errorf("second query = %q, want remaining limit and opaque continuation", request.URL.RawQuery)
			}
			writeTable(t, writer, "", tableRow("apps", "second", "3"))
		default:
			t.Errorf("unexpected table request %d", requests)
		}
	}))
	defer server.Close()

	printer, err := newTablePrinter(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatalf("newTablePrinter() error = %v", err)
	}
	fields, err := printer(context.Background(), configMapSpec(), tableRequest{
		namespace: "apps",
		rowBudget: 3,
		retainKeys: map[string]struct{}{
			tableObjectKey("apps", "first"):  {},
			tableObjectKey("apps", "second"): {},
		},
	})
	if err != nil {
		t.Fatalf("printer() error = %v", err)
	}
	if requests != 2 || len(fields) != 2 || fields[tableObjectKey("apps", "first")][1].Value != "1" ||
		fields[tableObjectKey("apps", "second")][1].Value != "3" {
		t.Fatalf("requests/fields = %d/%#v, want two retained rows across two pages", requests, fields)
	}
	if _, retained := fields[tableObjectKey("apps", "ignored")]; retained {
		t.Fatalf("fields = %#v, query-excluded row was retained", fields)
	}
}

func TestTablePrinterRejectsServerThatIgnoresRowLimit(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writeTable(t, writer, "", tableRow("apps", "first", "1"), tableRow("apps", "second", "2"))
	}))
	defer server.Close()

	printer, err := newTablePrinter(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatalf("newTablePrinter() error = %v", err)
	}
	_, err = printer(context.Background(), configMapSpec(), tableRequest{namespace: "apps", rowBudget: 1})
	if err == nil || !strings.Contains(err.Error(), "ignored the requested row limit") {
		t.Fatalf("printer() error = %v, want ignored-limit rejection", err)
	}
}

func TestTablePrinterRejectsRepeatedContinuationWithoutRestart(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if requests == 2 && request.URL.Query().Get("continue") != "cycle" {
			t.Errorf("second continuation = %q, want cycle", request.URL.Query().Get("continue"))
		}
		writer.Header().Set("Content-Type", "application/json")
		writeTable(t, writer, "cycle", tableRow("apps", "row", "1"))
	}))
	defer server.Close()

	printer, err := newTablePrinter(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatalf("newTablePrinter() error = %v", err)
	}
	_, err = printer(context.Background(), configMapSpec(), tableRequest{namespace: "apps", rowBudget: 3})
	if err == nil || !strings.Contains(err.Error(), "repeated a continuation token") || requests != 2 {
		t.Fatalf("printer() requests/error = %d/%v, want fail-closed cycle detection", requests, err)
	}
}

func TestTablePrinterDoesNotRestartRejectedContinuationOrExposeBody(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		statusCode int
		reason     metav1.StatusReason
		wantError  string
	}{
		{name: "expired", statusCode: http.StatusGone, reason: metav1.StatusReasonExpired, wantError: "expired"},
		{name: "invalid", statusCode: http.StatusBadRequest, reason: metav1.StatusReasonBadRequest, wantError: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			const secretBody = "credential-like-response-body"
			const rejectedToken = "rejected-token"
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests++
				if requests == 1 {
					writer.Header().Set("Content-Type", "application/json")
					writeTable(t, writer, rejectedToken, tableRow("apps", "first", "1"))
					return
				}
				if request.URL.Query().Get("continue") != rejectedToken {
					t.Errorf("continuation = %q, want opaque rejected token", request.URL.Query().Get("continue"))
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.statusCode)
				_ = json.NewEncoder(writer).Encode(metav1.Status{
					TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
					Status:   metav1.StatusFailure, Reason: test.reason,
					Message: secretBody, Code: int32(test.statusCode), // #nosec G115 -- test cases use fixed HTTP constants.
				})
			}))
			defer server.Close()

			printer, err := newTablePrinter(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatalf("newTablePrinter() error = %v", err)
			}
			_, err = printer(context.Background(), configMapSpec(), tableRequest{namespace: "apps", rowBudget: 2})
			if err == nil || !strings.Contains(err.Error(), test.wantError) || strings.Contains(err.Error(), secretBody) ||
				strings.Contains(err.Error(), rejectedToken) || requests != 2 {
				t.Fatalf("printer() requests/error = %d/%v, want sanitized fail-closed rejection", requests, err)
			}
		})
	}
}

func TestTablePrinterBoundsResponsePageBytes(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(strings.Repeat("x", int(tableResponsePageByteLimit+1))))
	}))
	defer server.Close()

	printer, err := newTablePrinter(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatalf("newTablePrinter() error = %v", err)
	}
	_, err = printer(context.Background(), configMapSpec(), tableRequest{namespace: "apps", rowBudget: 1})
	if err == nil || !strings.Contains(err.Error(), "response page byte limit") {
		t.Fatalf("printer() error = %v, want bounded response rejection", err)
	}
}

func configMapSpec() resourceSpec {
	return resourceSpec{
		kind: "ConfigMap", gvr: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, namespaced: true,
	}
}

func tableRow(namespace, name, data string) metav1.TableRow {
	return metav1.TableRow{
		Cells: []any{name, data},
		Object: runtime.RawExtension{Raw: []byte(
			`{"metadata":{"name":"` + name + `","namespace":"` + namespace + `"}}`,
		)},
	}
}

func writeTable(t *testing.T, writer http.ResponseWriter, continueToken string, rows ...metav1.TableRow) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(metav1.Table{
		TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "Table"},
		ListMeta: metav1.ListMeta{Continue: continueToken},
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "Data", Type: "integer"},
		},
		Rows: rows,
	}); err != nil {
		t.Errorf("encode table: %v", err)
	}
}
