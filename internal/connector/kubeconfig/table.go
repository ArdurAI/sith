// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/ArdurAI/sith/internal/fleet"
)

const (
	tableAccept                  = "application/json;as=Table;g=meta.k8s.io;v=v1"
	tableResponsePageByteLimit   = int64(4 << 20)
	tableResponseTotalByteBudget = int64(16 << 20)
)

func newTablePrinter(config *rest.Config) (tablePrinter, error) {
	tableConfig := dynamic.ConfigFor(config)
	tableConfig.GroupVersion = nil
	tableConfig.APIPath = "/if-you-see-this-search-for-the-break"
	guardTableErrorResponses(tableConfig)
	client, err := rest.UnversionedRESTClientFor(tableConfig)
	if err != nil {
		return nil, fmt.Errorf("create table client: %w", err)
	}
	return func(
		ctx context.Context,
		spec resourceSpec,
		request tableRequest,
	) (map[string][]fleet.DisplayField, error) {
		return requestTable(ctx, client, spec, request)
	}, nil
}

func requestTable(
	ctx context.Context,
	client rest.Interface,
	spec resourceSpec,
	request tableRequest,
) (map[string][]fleet.DisplayField, error) {
	if request.name == "" && request.rowBudget <= 0 {
		return map[string][]fleet.DisplayField{}, nil
	}
	if request.retainKeys != nil && len(request.retainKeys) == 0 {
		return map[string][]fleet.DisplayField{}, nil
	}

	remainingKeys := cloneTableKeys(request.retainKeys)
	resultCapacity := request.rowBudget
	if remainingKeys != nil {
		resultCapacity = min(resultCapacity, len(remainingKeys))
	}
	if request.name != "" {
		resultCapacity = 1
	}
	result := make(map[string][]fleet.DisplayField, max(resultCapacity, 0))
	continueToken := ""
	seenTokens := make(map[string]struct{})
	rowsRead := 0
	bytesRead := int64(0)
	for page := 0; page < queryListPageBudget; page++ {
		remainingRows := request.rowBudget - rowsRead
		pageLimit := int64(0)
		if request.name == "" {
			if remainingRows <= 0 {
				return result, nil
			}
			pageLimit = min(queryListPageSize, int64(remainingRows))
		}
		remainingBytes := tableResponseTotalByteBudget - bytesRead
		if remainingBytes <= 0 {
			return nil, fmt.Errorf("server table for %s exceeded the total response byte budget", spec.kind)
		}
		table, pageBytes, err := requestTablePage(
			ctx, client, spec, request, pageLimit, continueToken, min(tableResponsePageByteLimit, remainingBytes),
		)
		if err != nil {
			return nil, err
		}
		bytesRead += pageBytes
		if len(table.ColumnDefinitions) == 0 {
			return nil, fmt.Errorf("server table for %s has no column definitions", spec.kind)
		}
		if request.name == "" && int64(len(table.Rows)) > pageLimit {
			return nil, fmt.Errorf("server table for %s ignored the requested row limit", spec.kind)
		}
		rowsRead += len(table.Rows)
		for _, row := range table.Rows {
			rowNamespace, rowName := tableRowIdentity(row, table.ColumnDefinitions, request.namespace)
			if rowName == "" {
				continue
			}
			key := tableObjectKey(rowNamespace, rowName)
			if remainingKeys != nil {
				if _, retain := remainingKeys[key]; !retain {
					continue
				}
				delete(remainingKeys, key)
			}
			result[key] = tableDisplayFields(row, table.ColumnDefinitions)
		}
		if request.name != "" || (remainingKeys != nil && len(remainingKeys) == 0) {
			return result, nil
		}
		next := table.GetContinue()
		if next == "" {
			return result, nil
		}
		if _, repeated := seenTokens[next]; repeated {
			return nil, fmt.Errorf("server table for %s repeated a continuation token", spec.kind)
		}
		seenTokens[next] = struct{}{}
		continueToken = next
		if rowsRead >= request.rowBudget {
			return result, nil
		}
	}
	return nil, fmt.Errorf("server table for %s exceeded the page budget", spec.kind)
}

func requestTablePage(
	ctx context.Context,
	client rest.Interface,
	spec resourceSpec,
	tableRequest tableRequest,
	limit int64,
	continueToken string,
	byteLimit int64,
) (metav1.Table, int64, error) {
	segments := tableURLSegments(spec, tableRequest.namespace, tableRequest.name)
	request := client.Get().AbsPath(segments...).SetHeader("Accept", tableAccept)
	if tableRequest.name == "" {
		request = request.VersionedParams(&metav1.ListOptions{
			LabelSelector: tableRequest.labelSelector,
			Limit:         limit,
			Continue:      continueToken,
		}, metav1.ParameterCodec)
	} else {
		request = request.VersionedParams(&metav1.GetOptions{}, metav1.ParameterCodec)
	}
	stream, err := request.Stream(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return metav1.Table{}, 0, fmt.Errorf("request server table for %s: %w", spec.kind, ctxErr)
		}
		if apierrors.IsResourceExpired(err) {
			return metav1.Table{}, 0, fmt.Errorf("server table continuation for %s expired", spec.kind)
		}
		return metav1.Table{}, 0, fmt.Errorf("request server table for %s failed", spec.kind)
	}
	defer func() { _ = stream.Close() }()
	payload, err := io.ReadAll(io.LimitReader(stream, byteLimit+1))
	if err != nil {
		return metav1.Table{}, 0, fmt.Errorf("read server table for %s: %w", spec.kind, err)
	}
	if int64(len(payload)) > byteLimit {
		return metav1.Table{}, 0, fmt.Errorf("server table for %s exceeded the response page byte limit", spec.kind)
	}
	var table metav1.Table
	if err := json.Unmarshal(payload, &table); err != nil {
		return metav1.Table{}, 0, fmt.Errorf("decode server table for %s: %w", spec.kind, err)
	}
	return table, int64(len(payload)), nil
}

func cloneTableKeys(keys map[string]struct{}) map[string]struct{} {
	if keys == nil {
		return nil
	}
	cloned := make(map[string]struct{}, len(keys))
	for key := range keys {
		cloned[key] = struct{}{}
	}
	return cloned
}

func tableDisplayFields(
	row metav1.TableRow,
	columns []metav1.TableColumnDefinition,
) []fleet.DisplayField {
	fields := make([]fleet.DisplayField, 0, min(len(row.Cells), len(columns)))
	for index, cell := range row.Cells {
		if index >= len(columns) {
			break
		}
		column := columns[index]
		fields = append(fields, fleet.DisplayField{
			Name: column.Name, Value: tableCellString(cell), Priority: column.Priority,
		})
	}
	return fields
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
