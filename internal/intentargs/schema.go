// SPDX-License-Identifier: Apache-2.0

// Package intentargs compiles and validates bounded, exact argument schemas for governed intents.
package intentargs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
)

const (
	// Draft2020 is the only schema dialect accepted for governed intent arguments.
	Draft2020 = "https://json-schema.org/draft/2020-12/schema"
	// MaxDocumentBytes bounds both registered schemas and untrusted argument documents.
	MaxDocumentBytes = 64 * 1024
	// MaxNestingDepth prevents deeply nested JSON from exhausting parser stacks.
	MaxNestingDepth         = 32
	maxNumberBytes          = 128
	maxExponent             = 1000
	maxSchemaNodes          = 256
	maxSchemaDepth          = 16
	maxAnnotationBytes      = 512
	maxAnnotationBytesTotal = 4 * 1024
)

// ErrInvalidArgs is returned without echoing untrusted argument values.
var ErrInvalidArgs = errors.New("intent args are invalid")

// Schema is an immutable, locally resolved Draft 2020-12 argument schema.
type Schema struct {
	raw      json.RawMessage
	resolved *jsonschema.Resolved
}

// Compile validates and locally resolves one exact object schema. Remote references and schema
// features that this validator does not enforce are rejected rather than silently ignored.
func Compile(document json.RawMessage) (*Schema, error) {
	if _, err := decodeExactObject(document); err != nil {
		return nil, fmt.Errorf("compile intent args schema: %w", err)
	}

	var parsed jsonschema.Schema
	if err := json.Unmarshal(document, &parsed); err != nil {
		return nil, fmt.Errorf("compile intent args schema: decode schema: %w", err)
	}
	if parsed.Schema != Draft2020 {
		return nil, fmt.Errorf("compile intent args schema: schema dialect must be Draft 2020-12")
	}
	if parsed.Type != "object" || len(parsed.Types) != 0 || parsed.Ref != "" || parsed.DynamicRef != "" {
		return nil, fmt.Errorf("compile intent args schema: root must be a direct object schema")
	}
	budget := schemaBudget{}
	if err := validateSchemaPolicy(&parsed, true, 1, &budget); err != nil {
		return nil, fmt.Errorf("compile intent args schema: %w", err)
	}

	resolved, err := parsed.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("compile intent args schema: resolve local schema: %w", err)
	}
	return &Schema{raw: append(json.RawMessage(nil), document...), resolved: resolved}, nil
}

// Validate strictly parses and validates one argument object. Error text never contains input.
func (schema *Schema) Validate(document json.RawMessage) error {
	if schema == nil || schema.resolved == nil {
		return fmt.Errorf("%w: schema is unavailable", ErrInvalidArgs)
	}
	value, err := decodeExactObject(document)
	if err != nil {
		return fmt.Errorf("%w: malformed document", ErrInvalidArgs)
	}
	if err := schema.resolved.Validate(value); err != nil {
		return fmt.Errorf("%w: schema mismatch", ErrInvalidArgs)
	}
	return nil
}

// JSON returns a defensive copy of the registered schema document.
func (schema *Schema) JSON() json.RawMessage {
	if schema == nil {
		return nil
	}
	return append(json.RawMessage(nil), schema.raw...)
}

func decodeExactObject(document json.RawMessage) (map[string]any, error) {
	if len(document) == 0 {
		return nil, fmt.Errorf("JSON document is empty")
	}
	if len(document) > MaxDocumentBytes {
		return nil, fmt.Errorf("JSON document exceeds %d bytes", MaxDocumentBytes)
	}
	if !utf8.Valid(document) {
		return nil, fmt.Errorf("JSON document is not valid UTF-8")
	}

	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("read JSON root: %w", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("JSON root must be an object")
	}
	if err := inspectObject(decoder, 1); err != nil {
		return nil, err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return nil, fmt.Errorf("JSON document contains trailing data")
	}

	decoder = json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode JSON object: %w", err)
	}
	if _, err := normalizeNumbers(value); err != nil {
		return nil, err
	}
	return value, nil
}

func normalizeNumbers(value any) (any, error) {
	switch typed := value.(type) {
	case json.Number:
		number, ok := new(big.Rat).SetString(typed.String())
		if !ok || !number.IsInt() || !number.Num().IsInt64() {
			return nil, fmt.Errorf("JSON number must be an exact signed 64-bit integer")
		}
		return number.Num().Int64(), nil
	case []any:
		for index, child := range typed {
			normalized, err := normalizeNumbers(child)
			if err != nil {
				return nil, err
			}
			typed[index] = normalized
		}
	case map[string]any:
		for name, child := range typed {
			normalized, err := normalizeNumbers(child)
			if err != nil {
				return nil, err
			}
			typed[name] = normalized
		}
	}
	return value, nil
}

func inspectObject(decoder *json.Decoder, depth int) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("read JSON object member: %w", err)
		}
		name, ok := token.(string)
		if !ok {
			return fmt.Errorf("JSON object member name is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("JSON object contains a duplicate member")
		}
		seen[name] = struct{}{}
		if err := inspectValue(decoder, depth); err != nil {
			return err
		}
	}
	return closeContainer(decoder, '}')
}

func inspectArray(decoder *json.Decoder, depth int) error {
	for decoder.More() {
		if err := inspectValue(decoder, depth); err != nil {
			return err
		}
	}
	return closeContainer(decoder, ']')
}

func inspectValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read JSON value: %w", err)
	}
	if number, ok := token.(json.Number); ok {
		return validateNumber(number)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if depth >= MaxNestingDepth {
		return fmt.Errorf("JSON nesting exceeds %d levels", MaxNestingDepth)
	}
	switch delimiter {
	case '{':
		return inspectObject(decoder, depth+1)
	case '[':
		return inspectArray(decoder, depth+1)
	default:
		return fmt.Errorf("JSON value contains an invalid delimiter")
	}
}

func closeContainer(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("close JSON container: %w", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != want {
		return fmt.Errorf("JSON container has an invalid closing delimiter")
	}
	return nil
}

func validateNumber(number json.Number) error {
	text := string(number)
	if len(text) > maxNumberBytes {
		return fmt.Errorf("JSON number exceeds %d bytes", maxNumberBytes)
	}
	index := strings.IndexAny(text, "eE")
	if index < 0 {
		return nil
	}
	exponent, err := strconv.Atoi(text[index+1:])
	if err != nil || exponent < -maxExponent || exponent > maxExponent {
		return fmt.Errorf("JSON number exponent is out of bounds")
	}
	return nil
}

type schemaBudget struct {
	nodes           int
	annotationBytes int
}

func validateSchemaPolicy(schema *jsonschema.Schema, root bool, depth int, budget *schemaBudget) error {
	if schema == nil {
		return nil
	}
	budget.nodes++
	if budget.nodes > maxSchemaNodes {
		return fmt.Errorf("schema exceeds %d nodes", maxSchemaNodes)
	}
	if depth > maxSchemaDepth {
		return fmt.Errorf("schema exceeds %d levels", maxSchemaDepth)
	}
	if schema.ID != "" {
		return fmt.Errorf("schema IDs are not allowed")
	}
	if schema.Ref != "" && !strings.HasPrefix(schema.Ref, "#") {
		return fmt.Errorf("remote schema references are not allowed")
	}
	if schema.DynamicRef != "" && !strings.HasPrefix(schema.DynamicRef, "#") {
		return fmt.Errorf("remote dynamic schema references are not allowed")
	}
	if schema.Format != "" || schema.ContentEncoding != "" || schema.ContentMediaType != "" || schema.ContentSchema != nil {
		return fmt.Errorf("non-enforcing format and content keywords are not allowed")
	}
	if len(schema.Extra) != 0 {
		return fmt.Errorf("unsupported schema keywords are not allowed")
	}
	if !root && schema.Schema != "" {
		return fmt.Errorf("nested schema dialect declarations are not allowed")
	}
	if len(schema.Vocabulary) != 0 {
		return fmt.Errorf("custom schema vocabularies are not allowed")
	}
	if schema.Comment != "" || len(schema.Default) != 0 || len(schema.Examples) != 0 {
		return fmt.Errorf("schema comments, defaults, and examples are not allowed")
	}
	if err := validateAnnotation("title", schema.Title, budget); err != nil {
		return err
	}
	if err := validateAnnotation("description", schema.Description, budget); err != nil {
		return err
	}
	if schemaMayMatchObject(schema) && !isFalseSchema(schema.AdditionalProperties) {
		return fmt.Errorf("every object schema must set additionalProperties to false")
	}

	for _, child := range childSchemas(schema) {
		if err := validateSchemaPolicy(child, false, depth+1, budget); err != nil {
			return err
		}
	}
	return nil
}

func validateAnnotation(label, value string, budget *schemaBudget) error {
	if value == "" {
		return nil
	}
	budget.annotationBytes += len(value)
	if len(value) > maxAnnotationBytes || budget.annotationBytes > maxAnnotationBytesTotal ||
		!utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("schema %s is not a bounded annotation", label)
	}
	for _, character := range value {
		if unicode.IsControl(character) || strings.ContainsRune("<>{}", character) {
			return fmt.Errorf("schema %s contains unsafe annotation content", label)
		}
	}
	return nil
}

func schemaMayMatchObject(schema *jsonschema.Schema) bool {
	if schema.Type == "object" || len(schema.Properties) != 0 || len(schema.PatternProperties) != 0 ||
		schema.AdditionalProperties != nil || schema.PropertyNames != nil || len(schema.DependentSchemas) != 0 ||
		len(schema.DependentRequired) != 0 || schema.MinProperties != nil || schema.MaxProperties != nil {
		return true
	}
	for _, schemaType := range schema.Types {
		if schemaType == "object" {
			return true
		}
	}
	return false
}

func isFalseSchema(schema *jsonschema.Schema) bool {
	want := &jsonschema.Schema{Not: &jsonschema.Schema{}}
	return schema != nil && jsonschema.Equal(schema, want)
}

func childSchemas(schema *jsonschema.Schema) []*jsonschema.Schema {
	children := make([]*jsonschema.Schema, 0)
	appendMap := func(values map[string]*jsonschema.Schema) {
		for _, child := range values {
			children = append(children, child)
		}
	}
	appendMap(schema.Defs)
	appendMap(schema.Definitions)
	appendMap(schema.DependencySchemas)
	appendMap(schema.Properties)
	appendMap(schema.PatternProperties)
	appendMap(schema.DependentSchemas)
	children = append(children, schema.PrefixItems...)
	children = append(children, schema.Items)
	children = append(children, schema.ItemsArray...)
	children = append(children, schema.AdditionalItems, schema.Contains, schema.UnevaluatedItems)
	children = append(children, schema.AdditionalProperties, schema.PropertyNames, schema.UnevaluatedProperties)
	children = append(children, schema.AllOf...)
	children = append(children, schema.AnyOf...)
	children = append(children, schema.OneOf...)
	children = append(children, schema.Not, schema.If, schema.Then, schema.Else, schema.ContentSchema)
	return children
}
