// SPDX-License-Identifier: Apache-2.0

package kubeconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/ArdurAI/sith/internal/connector"
)

const (
	maxImportFiles = 128
	maxImportBytes = 4 << 20
	maxImportDepth = 8
)

var errImportLimit = errors.New("kubeconfig directory import limit reached")

type importedConfig struct {
	raw         *clientcmdapi.Config
	metadata    map[string]contextMetadata
	diagnostics []connector.Diagnostic
}

type contextMetadata struct {
	displayName string
	origin      string
}

// loadDirectory imports independently parsed kubeconfig files without following symlinks. The
// returned config is namespaced by an opaque per-file identifier so duplicate context names cannot
// silently shadow one another.
func loadDirectory(root string) (importedConfig, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return importedConfig{}, fmt.Errorf("kubeconfig directory is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return importedConfig{}, errors.New("cannot resolve kubeconfig directory")
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return importedConfig{}, errors.New("cannot inspect kubeconfig directory")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return importedConfig{}, fmt.Errorf("kubeconfig directory must be a real directory, not a symlink or file")
	}

	result := importedConfig{
		raw:      clientcmdapi.NewConfig(),
		metadata: make(map[string]contextMetadata),
	}
	candidates := 0
	entries := 0
	err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
		relative, relativeErr := filepath.Rel(absolute, path)
		if relativeErr != nil {
			return errors.New("cannot scan kubeconfig directory")
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			if walkErr != nil {
				return errors.New("cannot read kubeconfig directory")
			}
			return nil
		}
		entries++
		if entries > maxImportFiles {
			result.diagnostics = append(result.diagnostics, importDiagnostic("", "kubeconfig entry limit reached"))
			return errImportLimit
		}
		if walkErr != nil {
			result.diagnostics = append(result.diagnostics, importDiagnostic(relative, "unreadable entry"))
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry == nil {
			result.diagnostics = append(result.diagnostics, importDiagnostic(relative, "unreadable entry"))
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			result.diagnostics = append(result.diagnostics, importDiagnostic(relative, "symlink input ignored"))
			return nil
		}
		if entry.IsDir() {
			if importDepth(relative) > maxImportDepth {
				result.diagnostics = append(result.diagnostics, importDiagnostic(relative, "directory depth limit reached"))
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		candidates++
		if candidates > maxImportFiles {
			result.diagnostics = append(result.diagnostics, importDiagnostic("", "kubeconfig entry limit reached"))
			return errImportLimit
		}
		fileInfo, statErr := entry.Info()
		if statErr != nil || fileInfo.Size() > maxImportBytes {
			message := "unreadable kubeconfig"
			if statErr == nil {
				message = "kubeconfig exceeds the import size limit"
			}
			result.diagnostics = append(result.diagnostics, importDiagnostic(relative, message))
			return nil
		}
		config, readErr := loadKubeconfigFile(path, fileInfo.Size())
		if readErr != nil {
			result.diagnostics = append(result.diagnostics, importDiagnostic(relative, "invalid kubeconfig"))
			return nil
		}
		mergeImportedConfig(result.raw, result.metadata, &result.diagnostics, config, relative)
		return nil
	})
	if errors.Is(err, errImportLimit) {
		err = nil
	}
	if err != nil {
		return importedConfig{}, errors.New("cannot scan kubeconfig directory")
	}
	sort.Slice(result.diagnostics, func(left, right int) bool {
		if result.diagnostics[left].Source == result.diagnostics[right].Source {
			return result.diagnostics[left].Message < result.diagnostics[right].Message
		}
		return result.diagnostics[left].Source < result.diagnostics[right].Source
	})
	return result, nil
}

func loadKubeconfigFile(path string, size int64) (*clientcmdapi.Config, error) {
	if size < 0 || size > maxImportBytes {
		return nil, fmt.Errorf("kubeconfig exceeds the import size limit")
	}
	file, err := os.Open(path) // #nosec G304 -- path is discovered beneath a user-selected directory without symlink traversal.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	payload, err := io.ReadAll(io.LimitReader(file, maxImportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxImportBytes {
		return nil, fmt.Errorf("kubeconfig exceeds the import size limit")
	}
	config, err := clientcmd.Load(payload)
	if err != nil {
		return nil, err
	}
	setLocationOfOrigin(config, path)
	if err := clientcmd.ResolveLocalPaths(config); err != nil {
		return nil, err
	}
	return config, nil
}

func setLocationOfOrigin(config *clientcmdapi.Config, path string) {
	for _, authInfo := range config.AuthInfos {
		authInfo.LocationOfOrigin = path
	}
	for _, cluster := range config.Clusters {
		cluster.LocationOfOrigin = path
	}
	for _, context := range config.Contexts {
		context.LocationOfOrigin = path
	}
}

func mergeImportedConfig(
	destination *clientcmdapi.Config,
	metadata map[string]contextMetadata,
	diagnostics *[]connector.Diagnostic,
	source *clientcmdapi.Config,
	origin string,
) {
	identifier := importIdentifier(origin)
	clusters := make(map[string]string, len(source.Clusters))
	for _, name := range sortedMapKeys(source.Clusters) {
		qualified := identifier + "/cluster/" + name
		clusters[name] = qualified
		destination.Clusters[qualified] = source.Clusters[name].DeepCopy()
	}
	users := make(map[string]string, len(source.AuthInfos))
	for _, name := range sortedMapKeys(source.AuthInfos) {
		qualified := identifier + "/user/" + name
		users[name] = qualified
		destination.AuthInfos[qualified] = source.AuthInfos[name].DeepCopy()
	}
	for _, name := range sortedMapKeys(source.Contexts) {
		qualified := identifier + "/context/" + name
		context := source.Contexts[name]
		if context == nil {
			*diagnostics = append(*diagnostics, importDiagnostic(origin, "kubeconfig contains an invalid context"))
			continue
		}
		cluster, clusterExists := clusters[context.Cluster]
		if !clusterExists {
			*diagnostics = append(*diagnostics, importDiagnostic(origin, "context references an unavailable cluster"))
			continue
		}
		user := ""
		if context.AuthInfo != "" {
			var userExists bool
			user, userExists = users[context.AuthInfo]
			if !userExists {
				*diagnostics = append(*diagnostics, importDiagnostic(origin, "context references an unavailable user"))
				continue
			}
		}
		context = context.DeepCopy()
		context.Cluster = cluster
		context.AuthInfo = user
		destination.Contexts[qualified] = context
		metadata[qualified] = contextMetadata{displayName: name, origin: origin}
	}
}

func importIdentifier(origin string) string {
	digest := sha256.Sum256([]byte(origin))
	return "import-" + hex.EncodeToString(digest[:8])
}

func sortedMapKeys[T any](values map[string]*T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func importDepth(relative string) int {
	if relative == "" || relative == "." {
		return 0
	}
	return len(strings.Split(relative, "/"))
}

func importDiagnostic(source, message string) connector.Diagnostic {
	return connector.Diagnostic{Source: source, Message: message}
}
