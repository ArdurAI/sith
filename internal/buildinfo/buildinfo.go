// SPDX-License-Identifier: Apache-2.0

// Package buildinfo exposes build metadata injected by the build pipeline.
package buildinfo

import (
	"encoding/json"
	"fmt"
	"runtime"
)

// Version is the semantic version or development identifier injected at build time.
var Version = "dev"

// Commit is the source commit injected at build time.
var Commit = "none"

// Date is the UTC build timestamp injected at build time.
var Date = "unknown"

// Info is the resolved build metadata, including runtime-derived fields.
type Info struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Date     string `json:"date"`
	Go       string `json:"go"`
	Platform string `json:"platform"`
}

// Get returns the build metadata combined with the active Go runtime and platform.
func Get() Info {
	return Info{
		Version:  Version,
		Commit:   Commit,
		Date:     Date,
		Go:       runtime.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// String returns build metadata in the human-readable CLI format.
func (i Info) String() string {
	return fmt.Sprintf(
		"sith %s\n  commit:    %s\n  built:     %s\n  go:        %s\n  platform:  %s",
		i.Version,
		i.Commit,
		i.Date,
		i.Go,
		i.Platform,
	)
}

// JSON returns build metadata as compact JSON.
func (i Info) JSON() (string, error) {
	data, err := json.Marshal(i)
	if err != nil {
		return "", fmt.Errorf("marshal build metadata: %w", err)
	}

	return string(data), nil
}
