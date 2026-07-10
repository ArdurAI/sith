// SPDX-License-Identifier: Apache-2.0

package buildinfo

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestGetPopulatesRuntimeFields(t *testing.T) {
	t.Parallel()

	got := Get()
	if got.Go != runtime.Version() {
		t.Fatalf("Go = %q, want %q", got.Go, runtime.Version())
	}

	wantPlatform := runtime.GOOS + "/" + runtime.GOARCH
	if got.Platform != wantPlatform {
		t.Fatalf("Platform = %q, want %q", got.Platform, wantPlatform)
	}
}

func TestStringContainsAllFields(t *testing.T) {
	t.Parallel()

	info := Info{
		Version:  "v1.2.3",
		Commit:   "abc1234",
		Date:     "2026-07-10T12:00:00Z",
		Go:       "go1.25.12",
		Platform: "linux/amd64",
	}

	for _, want := range []string{info.Version, info.Commit, info.Date, info.Go, info.Platform} {
		if !strings.Contains(info.String(), want) {
			t.Errorf("String() = %q, want it to contain %q", info.String(), want)
		}
	}
}

func TestJSONRoundTrips(t *testing.T) {
	t.Parallel()

	want := Info{
		Version:  "v1.2.3",
		Commit:   "abc1234",
		Date:     "2026-07-10T12:00:00Z",
		Go:       "go1.25.12",
		Platform: "linux/amd64",
	}

	encoded, err := want.JSON()
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}

	var got Info
	if err := json.Unmarshal([]byte(encoded), &got); err != nil {
		t.Fatalf("unmarshal JSON: %v", err)
	}

	if got != want {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}
