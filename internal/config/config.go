// SPDX-License-Identifier: Apache-2.0

// Package config loads and validates local Sith configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

const maxConfigBytes = 1 << 20

// Config contains local-mode process settings.
type Config struct {
	LogLevel       string `yaml:"log_level"`
	LogFormat      string `yaml:"log_format"`
	KubeconfigPath string `yaml:"kubeconfig_path"`
}

// Overrides contains non-empty command-line values that take final precedence.
type Overrides struct {
	LogLevel  string
	LogFormat string
}

// Defaults returns the safe local-mode defaults.
func Defaults() Config {
	return Config{
		LogLevel:  "info",
		LogFormat: "text",
	}
}

// Load resolves defaults, an optional YAML file, environment variables, and flag overrides.
func Load(path string, overrides Overrides) (Config, error) {
	resolvedPath, explicit, err := resolvePath(path)
	if err != nil {
		return Config{}, err
	}

	resolved := Defaults()
	if err := mergeFile(resolvedPath, explicit, &resolved); err != nil {
		return Config{}, err
	}

	applyEnvironment(&resolved)
	applyOverrides(overrides, &resolved)

	if err := resolved.Validate(); err != nil {
		return Config{}, err
	}

	return resolved, nil
}

// Validate rejects unknown logging values rather than silently weakening behavior.
func (c Config) Validate() error {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log level %q: expected debug, info, warn, or error", c.LogLevel)
	}

	switch c.LogFormat {
	case "text", "json":
	default:
		return fmt.Errorf("invalid log format %q: expected text or json", c.LogFormat)
	}

	return nil
}

func resolvePath(path string) (resolved string, explicit bool, err error) {
	if path != "" {
		return path, true, nil
	}

	if root := os.Getenv("XDG_CONFIG_HOME"); root != "" {
		return filepath.Join(root, "sith", "config.yaml"), false, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("resolve home directory: %w", err)
	}

	return filepath.Join(home, ".config", "sith", "config.yaml"), false, nil
}

func mergeFile(path string, explicit bool, resolved *Config) error {
	data, err := readConfig(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicit {
			return nil
		}

		return fmt.Errorf("read config %q: %w", path, err)
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(resolved); err != nil {
		return fmt.Errorf("decode config %q: %w", path, err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode config %q: multiple YAML documents are not supported", path)
		}

		return fmt.Errorf("decode config %q: %w", path, err)
	}

	return nil
}

func readConfig(path string) ([]byte, error) {
	// The path is explicitly selected by the local user or resolved under their config directory.
	file, err := os.Open(path) //nolint:gosec // reading that user-selected path is the intended behavior
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	if info.Size() > maxConfigBytes {
		return nil, fmt.Errorf("file is %d bytes, maximum is %d", info.Size(), maxConfigBytes)
	}

	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if len(data) > maxConfigBytes {
		return nil, fmt.Errorf("file exceeds maximum size of %d bytes", maxConfigBytes)
	}

	return data, nil
}

func applyEnvironment(resolved *Config) {
	if value := os.Getenv("SITH_LOG_LEVEL"); value != "" {
		resolved.LogLevel = value
	}
	if value := os.Getenv("SITH_LOG_FORMAT"); value != "" {
		resolved.LogFormat = value
	}
	if value := os.Getenv("SITH_KUBECONFIG"); value != "" {
		resolved.KubeconfigPath = value
	}
}

func applyOverrides(overrides Overrides, resolved *Config) {
	if overrides.LogLevel != "" {
		resolved.LogLevel = overrides.LogLevel
	}
	if overrides.LogFormat != "" {
		resolved.LogFormat = overrides.LogFormat
	}
}
