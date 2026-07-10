// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
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
	}, "apps", "", "app=sample")
	if err != nil {
		t.Fatalf("printer() error = %v", err)
	}
	got := fields[tableObjectKey("apps", "settings")]
	if len(got) != 3 || got[1].Value != "2" || got[2].Priority != 1 ||
		!slices.Equal([]string{got[0].Name, got[1].Name, got[2].Name}, []string{"Name", "Data", "Age"}) {
		t.Fatalf("fields = %#v", got)
	}
}
