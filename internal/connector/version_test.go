// SPDX-License-Identifier: Apache-2.0

package connector

import (
	"errors"
	"testing"
)

func TestNegotiateWireVersion(t *testing.T) {
	t.Parallel()

	v10 := WireVersion{Major: 1, Minor: 0}
	v11 := WireVersion{Major: 1, Minor: 1}
	v12 := WireVersion{Major: 1, Minor: 2}
	v20 := WireVersion{Major: 2, Minor: 0}
	oversized := make([]WireVersion, maxWireVersionsPerOffer+1)
	for index := range oversized {
		oversized[index] = WireVersion{Major: 1, Minor: uint32(index)}
	}
	tests := []struct {
		name   string
		local  []WireVersion
		remote []WireVersion
		want   WireVersion
		isErr  error
	}{
		{name: "initial version", local: []WireVersion{v10}, remote: []WireVersion{v10}, want: v10},
		{name: "highest explicit minor", local: []WireVersion{v10, v11, v12}, remote: []WireVersion{v11, v10}, want: v11},
		{name: "highest explicit major", local: []WireVersion{v20, v10}, remote: []WireVersion{v10, v20}, want: v20},
		{name: "order independent", local: []WireVersion{v12, v10, v11}, remote: []WireVersion{v10, v11}, want: v11},
		{name: "empty local offer", remote: []WireVersion{v10}, isErr: ErrInvalidWireVersions},
		{name: "empty remote offer", local: []WireVersion{v10}, isErr: ErrInvalidWireVersions},
		{name: "zero major", local: []WireVersion{{Minor: 1}}, remote: []WireVersion{v10}, isErr: ErrInvalidWireVersions},
		{name: "duplicate local", local: []WireVersion{v10, v10}, remote: []WireVersion{v10}, isErr: ErrInvalidWireVersions},
		{name: "duplicate remote", local: []WireVersion{v10}, remote: []WireVersion{v10, v10}, isErr: ErrInvalidWireVersions},
		{name: "oversized local offer", local: oversized, remote: []WireVersion{v10}, isErr: ErrInvalidWireVersions},
		{name: "major mismatch", local: []WireVersion{v10, v11}, remote: []WireVersion{v20}, isErr: ErrWireMajorMismatch},
		{name: "minor not explicit", local: []WireVersion{v10}, remote: []WireVersion{v11}, isErr: ErrWireMinorUnsupported},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := NegotiateWireVersion(test.local, test.remote)
			if test.isErr != nil {
				if !errors.Is(err, test.isErr) {
					t.Fatalf("NegotiateWireVersion() error = %v, want %v", err, test.isErr)
				}
				if got != (WireVersion{}) {
					t.Fatalf("NegotiateWireVersion() = %v on error, want zero", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("NegotiateWireVersion() = %v, %v, want %v", got, err, test.want)
			}
		})
	}
}

func TestCurrentWireVersion(t *testing.T) {
	t.Parallel()

	if got := CurrentWireVersion(); got != (WireVersion{Major: 1, Minor: 0}) || got.String() != "1.0" {
		t.Fatalf("CurrentWireVersion() = %v (%q)", got, got.String())
	}
}

func FuzzNegotiateWireVersionNeverReturnsAnUnofferedVersion(f *testing.F) {
	f.Add([]byte{1, 0, 1, 1}, []byte{1, 0})
	f.Add([]byte{2, 0}, []byte{1, 0})
	f.Add([]byte{}, []byte{1, 0})
	f.Fuzz(func(t *testing.T, localBytes, remoteBytes []byte) {
		local := wireVersionsFromBytes(localBytes)
		remote := wireVersionsFromBytes(remoteBytes)
		got, err := NegotiateWireVersion(local, remote)
		if err != nil {
			if got != (WireVersion{}) {
				t.Fatalf("error %v returned non-zero version %v", err, got)
			}
			return
		}
		if got.Major == 0 || !containsWireVersion(local, got) || !containsWireVersion(remote, got) {
			t.Fatalf("negotiated version %v was not explicitly offered by both sides", got)
		}
		for _, localVersion := range local {
			if !wireVersionLess(got, localVersion) || !containsWireVersion(remote, localVersion) {
				continue
			}
			t.Fatalf("negotiated %v instead of higher common version %v", got, localVersion)
		}
	})
}

func wireVersionsFromBytes(input []byte) []WireVersion {
	versions := make([]WireVersion, 0, len(input)/2)
	for offset := 0; offset+1 < len(input) && len(versions) < 32; offset += 2 {
		versions = append(versions, WireVersion{Major: uint32(input[offset]), Minor: uint32(input[offset+1])})
	}
	return versions
}

func containsWireVersion(versions []WireVersion, want WireVersion) bool {
	for _, version := range versions {
		if version == want {
			return true
		}
	}
	return false
}
