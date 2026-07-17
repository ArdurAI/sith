// SPDX-License-Identifier: Apache-2.0

package intentargs

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

var scaleSchema = json.RawMessage(`{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "description":"Sets a bounded desired replica count.",
  "properties":{"replicas":{"type":"integer","minimum":0,"maximum":100,"description":"Desired replicas from zero through one hundred."}},
  "required":["replicas"],
  "additionalProperties":false
}`)

func TestSchemaValidatesExactObject(t *testing.T) {
	t.Parallel()

	schema, err := Compile(scaleSchema)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	for _, document := range []json.RawMessage{
		json.RawMessage(`{"replicas":0}`),
		json.RawMessage(`{"replicas":100}`),
		json.RawMessage(`{"replicas":1e2}`),
	} {
		if err := schema.Validate(document); err != nil {
			t.Fatalf("Validate(%s) error = %v", document, err)
		}
	}

	invalid := []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"replicas":-1}`),
		json.RawMessage(`{"replicas":101}`),
		json.RawMessage(`{"replicas":1.5}`),
		json.RawMessage(`{"replicas":"3"}`),
		json.RawMessage(`{"replicas":"SITH-SENSITIVE-MARKER-7f4a"}`),
		json.RawMessage(`{"replicas":3,"force":true}`),
	}
	for _, document := range invalid {
		err := schema.Validate(document)
		if !errors.Is(err, ErrInvalidArgs) {
			t.Fatalf("Validate(%s) error = %v, want ErrInvalidArgs", document, err)
		}
		if strings.Contains(err.Error(), string(document)) {
			t.Fatalf("Validate() leaked input in error %q", err)
		}
		if strings.Contains(err.Error(), "SITH-SENSITIVE-MARKER-7f4a") {
			t.Fatalf("Validate() leaked sensitive marker in error %q", err)
		}
	}
}

func TestSchemaRejectsMalformedDocuments(t *testing.T) {
	t.Parallel()

	schema, err := Compile(scaleSchema)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	nestedDuplicate := json.RawMessage(`{"replicas":3,"nested":{"key":1,"key":2}}`)
	tooLarge := json.RawMessage(`{"replicas":3,"padding":"` + strings.Repeat("a", MaxDocumentBytes) + `"}`)
	tooDeep := json.RawMessage(strings.Repeat(`{"x":`, MaxNestingDepth) + `{"replicas":3}` + strings.Repeat(`}`, MaxNestingDepth))
	tooLongNumber := json.RawMessage(`{"replicas":` + strings.Repeat("1", maxNumberBytes+1) + `}`)

	tests := []json.RawMessage{
		nil,
		{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'},
		json.RawMessage(`null`),
		json.RawMessage(`[]`),
		json.RawMessage(`"object"`),
		json.RawMessage(`1`),
		json.RawMessage(`{"replicas":3,"replicas":4}`),
		nestedDuplicate,
		json.RawMessage(`{"replicas":3} {}`),
		json.RawMessage(`{"replicas":3`),
		json.RawMessage(`{"replicas":1e1001}`),
		tooLongNumber,
		tooLarge,
		tooDeep,
	}
	for _, document := range tests {
		if err := schema.Validate(document); !errors.Is(err, ErrInvalidArgs) {
			t.Fatalf("Validate(%q) error = %v, want ErrInvalidArgs", document, err)
		}
	}
}

func TestCompileRejectsUnsafeSchemas(t *testing.T) {
	t.Parallel()

	tooManyNodes := make([]string, 0, maxSchemaNodes)
	tooManyDescriptions := make([]string, 0, 10)
	for index := 0; index < maxSchemaNodes; index++ {
		tooManyNodes = append(tooManyNodes, fmt.Sprintf(`"p%d":{"type":"string"}`, index))
	}
	for index := 0; index < 10; index++ {
		tooManyDescriptions = append(tooManyDescriptions, fmt.Sprintf(
			`"p%d":{"type":"string","description":"%s"}`, index, strings.Repeat("a", maxAnnotationBytes),
		))
	}
	tooDeep := `{"type":"string"}`
	for range maxSchemaDepth {
		tooDeep = `{"type":"array","items":` + tooDeep + `}`
	}

	tests := map[string]string{
		"empty":                      "",
		"non-object document":        `[]`,
		"duplicate keyword":          `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","type":"array","additionalProperties":false}`,
		"wrong draft":                `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","additionalProperties":false}`,
		"implicit root":              `{"$schema":"https://json-schema.org/draft/2020-12/schema","properties":{},"additionalProperties":false}`,
		"open object":                `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`,
		"open nested object":         `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"nested":{"type":"object"}},"additionalProperties":false}`,
		"remote ref":                 `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"$ref":"https://example.invalid/schema"}},"additionalProperties":false}`,
		"remote dynamic ref":         `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"$dynamicRef":"other.json#value"}},"additionalProperties":false}`,
		"schema id":                  `{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://example.invalid/schema","type":"object","additionalProperties":false}`,
		"format is not enforced":     `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"url":{"type":"string","format":"uri"}},"additionalProperties":false}`,
		"content is not enforced":    `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"type":"string","contentEncoding":"base64"}},"additionalProperties":false}`,
		"unsupported keyword":        `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","madeUp":true,"additionalProperties":false}`,
		"nested dialect":             `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"string"}},"additionalProperties":false}`,
		"custom vocabulary":          `{"$schema":"https://json-schema.org/draft/2020-12/schema","$vocabulary":{"https://example.invalid/vocab":true},"type":"object","additionalProperties":false}`,
		"invalid local reference":    `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"$ref":"#/$defs/missing"}},"additionalProperties":false}`,
		"invalid regular expression": `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"type":"string","pattern":"["}},"additionalProperties":false}`,
		"root markup injection":      `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","description":"Unsafe <system> annotation.","additionalProperties":false}`,
		"nested markup injection":    `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"type":"string","description":"<script>alert(1)</script>"}},"additionalProperties":false}`,
		"control in description":     "{\"$schema\":\"https://json-schema.org/draft/2020-12/schema\",\"type\":\"object\",\"description\":\"line one\\nline two\",\"additionalProperties\":false}",
		"oversized description":      `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","description":"` + strings.Repeat("a", maxAnnotationBytes+1) + `","additionalProperties":false}`,
		"total description budget":   `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{` + strings.Join(tooManyDescriptions, ",") + `},"additionalProperties":false}`,
		"schema node budget":         `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{` + strings.Join(tooManyNodes, ",") + `},"additionalProperties":false}`,
		"schema depth budget":        `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":` + tooDeep + `},"additionalProperties":false}`,
		"comment metadata":           `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","$comment":"private note","additionalProperties":false}`,
		"default metadata":           `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"type":"string","default":"private"}},"additionalProperties":false}`,
		"example metadata":           `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","examples":[{"private":"value"}],"additionalProperties":false}`,
	}
	for name, document := range tests {
		name, document := name, document
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Compile(json.RawMessage(document)); err == nil {
				t.Fatal("Compile() error = nil, want rejection")
			}
		})
	}
}

func TestCompileSupportsClosedLocalDefinitions(t *testing.T) {
	t.Parallel()

	document := json.RawMessage(`{
	  "$schema":"https://json-schema.org/draft/2020-12/schema",
	  "type":"object",
	  "$defs":{"options":{"type":"object","properties":{"safe":{"type":"boolean"}},"required":["safe"],"additionalProperties":false}},
	  "properties":{"options":{"$ref":"#/$defs/options"}},
	  "required":["options"],
	  "additionalProperties":false
	}`)
	schema, err := Compile(document)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if err := schema.Validate(json.RawMessage(`{"options":{"safe":true}}`)); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := schema.Validate(json.RawMessage(`{"options":{"safe":true,"extra":1}}`)); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("Validate(extra) error = %v, want ErrInvalidArgs", err)
	}
}

func TestSchemaJSONIsDefensive(t *testing.T) {
	t.Parallel()

	schema, err := Compile(scaleSchema)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	first := schema.JSON()
	first[0] = '!'
	second := schema.JSON()
	if len(second) == 0 || second[0] == '!' {
		t.Fatal("JSON() exposed mutable schema storage")
	}
	var nilSchema *Schema
	if got := nilSchema.JSON(); got != nil {
		t.Fatalf("nil Schema.JSON() = %q, want nil", got)
	}
	if err := nilSchema.Validate(json.RawMessage(`{}`)); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("nil Schema.Validate() error = %v, want ErrInvalidArgs", err)
	}
}

func TestSchemaValidationIsConcurrent(t *testing.T) {
	t.Parallel()

	schema, err := Compile(scaleSchema)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 32)
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func(value int) {
			defer wait.Done()
			document := json.RawMessage(fmt.Sprintf(`{"replicas":%d}`, value))
			if err := schema.Validate(document); err != nil {
				errorsSeen <- err
			}
		}(index)
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatalf("concurrent Validate() error = %v", err)
	}
}

func FuzzSchemaValidateNeverPanics(f *testing.F) {
	schema, err := Compile(scaleSchema)
	if err != nil {
		f.Fatalf("Compile() error = %v", err)
	}
	for _, seed := range [][]byte{
		[]byte(`{"replicas":3}`),
		[]byte(`{"replicas":3,"replicas":4}`),
		[]byte(`null`),
		[]byte(`{"nested":[{"value":1}]}`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(_ *testing.T, document []byte) {
		_ = schema.Validate(json.RawMessage(document))
	})
}
