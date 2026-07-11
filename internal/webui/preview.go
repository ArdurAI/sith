// SPDX-License-Identifier: Apache-2.0

package webui

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/ArdurAI/sith/internal/localops"
)

const previewLifetime = 5 * time.Minute

type previewGrant struct {
	digest  [sha256.Size]byte
	expires time.Time
}

type previewManager struct {
	mu     sync.Mutex
	grants map[string]previewGrant
	now    func() time.Time
}

func newPreviewManager() *previewManager {
	return &previewManager{grants: make(map[string]previewGrant), now: time.Now}
}

func (manager *previewManager) issue(target localops.Target, manifest string) (string, error) {
	token, err := randomToken(24)
	if err != nil {
		return "", err
	}
	now := manager.now().UTC()
	manager.mu.Lock()
	manager.removeExpiredLocked(now)
	manager.grants[token] = previewGrant{digest: previewDigest(target, manifest), expires: now.Add(previewLifetime)}
	manager.mu.Unlock()
	return token, nil
}

func (manager *previewManager) consume(token string, target localops.Target, manifest string) error {
	now := manager.now().UTC()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.removeExpiredLocked(now)
	grant, exists := manager.grants[token]
	delete(manager.grants, token)
	if !exists || token == "" {
		return fmt.Errorf("a fresh server dry-run preview is required before apply")
	}
	if grant.digest != previewDigest(target, manifest) {
		return fmt.Errorf("the target or YAML changed after preview; preview again before apply")
	}
	return nil
}

func (manager *previewManager) clear() {
	manager.mu.Lock()
	manager.grants = make(map[string]previewGrant)
	manager.mu.Unlock()
}

func (manager *previewManager) removeExpiredLocked(now time.Time) {
	for token, grant := range manager.grants {
		if !grant.expires.After(now) {
			delete(manager.grants, token)
		}
	}
}

func previewDigest(target localops.Target, manifest string) [sha256.Size]byte {
	identity := target.Context + "\x00" + target.Namespace + "\x00" + target.Kind + "\x00" + target.Name + "\x00" + manifest
	return sha256.Sum256([]byte(identity))
}
