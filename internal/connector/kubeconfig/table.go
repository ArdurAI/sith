// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/ArdurAI/sith/internal/fleet"
)

const tableAccept = "application/json;as=Table;g=meta.k8s.io;v=v1"

func newTablePrinter(config *rest.Config) (tablePrinter, error) {
	tableConfig := dynamic.ConfigFor(config)
	tableConfig.GroupVersion = nil
	tableConfig.APIPath = "/if-you-see-this-search-for-the-break"
	client, err := rest.UnversionedRESTClientFor(tableConfig)
	if err != nil {
		return nil, fmt.Errorf("create table client: %w", err)
	}
	return func(
		ctx context.Context,
		spec resourceSpec,
		namespace, name, labelSelector string,
	) (map[string][]fleet.DisplayField, error) {
		return requestTable(ctx, client, spec, namespace, name, labelSelector)
	}, nil
}

func requestTable(
	ctx context.Context,
	client rest.Interface,
	spec resourceSpec,
	namespace, name, labelSelector string,
) (map[string][]fleet.DisplayField, error) {
	segments := tableURLSegments(spec, namespace, name)
	request := client.Get().AbsPath(segments...).SetHeader("Accept", tableAccept)
	if name == "" {
		request = request.VersionedParams(&metav1.ListOptions{LabelSelector: labelSelector}, metav1.ParameterCodec)
	} else {
		request = request.VersionedParams(&metav1.GetOptions{}, metav1.ParameterCodec)
	}
	payload, err := request.Do(ctx).Raw()
	if err != nil {
		return nil, fmt.Errorf("request server table for %s: %w", spec.kind, err)
	}
	var table metav1.Table
	if err := json.Unmarshal(payload, &table); err != nil {
		return nil, fmt.Errorf("decode server table for %s: %w", spec.kind, err)
	}
	if len(table.ColumnDefinitions) == 0 {
		return nil, fmt.Errorf("server table for %s has no column definitions", spec.kind)
	}
	result := make(map[string][]fleet.DisplayField, len(table.Rows))
	for _, row := range table.Rows {
		rowNamespace, rowName := tableRowIdentity(row, table.ColumnDefinitions, namespace)
		if rowName == "" {
			continue
		}
		fields := make([]fleet.DisplayField, 0, min(len(row.Cells), len(table.ColumnDefinitions)))
		for index, cell := range row.Cells {
			if index >= len(table.ColumnDefinitions) {
				break
			}
			column := table.ColumnDefinitions[index]
			fields = append(fields, fleet.DisplayField{
				Name: column.Name, Value: tableCellString(cell), Priority: column.Priority,
			})
		}
		result[tableObjectKey(rowNamespace, rowName)] = fields
	}
	return result, nil
}

func tableURLSegments(spec resourceSpec, namespace, name string) []string {
	segments := make([]string, 0, 7)
	if spec.gvr.Group == "" {
		segments = append(segments, "api", spec.gvr.Version)
	} else {
		segments = append(segments, "apis", spec.gvr.Group, spec.gvr.Version)
	}
	if spec.namespaced && namespace != "" {
		segments = append(segments, "namespaces", namespace)
	}
	segments = append(segments, spec.gvr.Resource)
	if name != "" {
		segments = append(segments, name)
	}
	return segments
}

func tableRowIdentity(
	row metav1.TableRow,
	columns []metav1.TableColumnDefinition,
	defaultNamespace string,
) (string, string) {
	namespace := defaultNamespace
	name := ""
	if len(row.Object.Raw) > 0 {
		var object struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		}
		if json.Unmarshal(row.Object.Raw, &object) == nil {
			name = object.Metadata.Name
			if object.Metadata.Namespace != "" {
				namespace = object.Metadata.Namespace
			}
		}
	}
	for index, column := range columns {
		if index >= len(row.Cells) {
			break
		}
		switch strings.ToLower(column.Name) {
		case "name":
			if name == "" {
				name = tableCellString(row.Cells[index])
			}
		case "namespace":
			if namespace == "" {
				namespace = tableCellString(row.Cells[index])
			}
		}
	}
	return namespace, name
}

func tableCellString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err == nil {
			return string(encoded)
		}
		return fmt.Sprint(typed)
	}
}

func tableObjectKey(namespace, name string) string {
	return namespace + "\x00" + name
}
