// SPDX-License-Identifier: Apache-2.0

package webui

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ArdurAI/sith/internal/localops"
)

const maxActiveForwards = 16

type forwardView struct {
	ID      string                   `json:"id"`
	Target  localops.Target          `json:"target"`
	Ports   []localops.ForwardedPort `json:"ports"`
	Started time.Time                `json:"started_at"`
	Done    bool                     `json:"done"`
	Error   string                   `json:"error,omitempty"`
}

type managedForward struct {
	view        forwardView
	session     localops.ForwardSession
	releaseOnce sync.Once
}

type forwardManager struct {
	mu       sync.RWMutex
	forwards map[string]*managedForward
	slots    chan struct{}
}

func newForwardManager() *forwardManager {
	return &forwardManager{
		forwards: make(map[string]*managedForward),
		slots:    make(chan struct{}, maxActiveForwards),
	}
}

func (manager *forwardManager) reserve() error {
	select {
	case manager.slots <- struct{}{}:
		return nil
	default:
		return fmt.Errorf("at most %d active port-forwards are allowed", maxActiveForwards)
	}
}

func (manager *forwardManager) releaseReservation() {
	<-manager.slots
}

func (manager *forwardManager) add(target localops.Target, session localops.ForwardSession) (forwardView, error) {
	id, err := randomToken(12)
	if err != nil {
		return forwardView{}, err
	}
	ports, err := session.Ports()
	if err != nil {
		return forwardView{}, fmt.Errorf("read forwarded ports: %w", err)
	}
	forward := &managedForward{
		view:    forwardView{ID: id, Target: target, Ports: ports, Started: time.Now().UTC()},
		session: session,
	}
	manager.mu.Lock()
	manager.forwards[id] = forward
	manager.mu.Unlock()
	go manager.monitor(forward)
	view := forward.view
	view.Ports = append([]localops.ForwardedPort(nil), view.Ports...)
	return view, nil
}

func (manager *forwardManager) monitor(forward *managedForward) {
	err := <-forward.session.Done()
	manager.mu.Lock()
	forward.view.Done = true
	if err != nil {
		forward.view.Error = err.Error()
	}
	if current, exists := manager.forwards[forward.view.ID]; exists && current == forward {
		delete(manager.forwards, forward.view.ID)
	}
	manager.mu.Unlock()
	forward.releaseOnce.Do(manager.releaseReservation)
}

func (manager *forwardManager) list() []forwardView {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	result := make([]forwardView, 0, len(manager.forwards))
	for _, forward := range manager.forwards {
		view := forward.view
		view.Ports = append([]localops.ForwardedPort(nil), view.Ports...)
		result = append(result, view)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Started.Before(result[right].Started) })
	return result
}

func (manager *forwardManager) close(id string) error {
	manager.mu.Lock()
	forward, exists := manager.forwards[id]
	if exists {
		delete(manager.forwards, id)
	}
	manager.mu.Unlock()
	if !exists {
		return fmt.Errorf("port-forward %q was not found", id)
	}
	closeErr := forward.session.Close()
	forward.releaseOnce.Do(manager.releaseReservation)
	return closeErr
}

func (manager *forwardManager) closeAll() error {
	manager.mu.Lock()
	forwards := manager.forwards
	manager.forwards = make(map[string]*managedForward)
	manager.mu.Unlock()
	errorsList := make([]error, 0, len(forwards))
	for _, forward := range forwards {
		errorsList = append(errorsList, forward.session.Close())
		forward.releaseOnce.Do(manager.releaseReservation)
	}
	return errors.Join(errorsList...)
}
