package testutil

import (
	"context"
	"sort"
	"sync"

	"github.com/jacobgad/dt241m-controller/internal/registry"
	"github.com/jacobgad/dt241m-controller/internal/store"
)

type MemoryStore struct {
	mu      sync.Mutex
	records map[string]registry.Adapter
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: make(map[string]registry.Adapter)}
}

func (m *MemoryStore) LoadAll(context.Context) ([]registry.Adapter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]registry.Adapter, 0, len(m.records))
	for _, a := range m.records {
		a.Online = false
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MAC < out[j].MAC })
	return out, nil
}

func (m *MemoryStore) Save(_ context.Context, a registry.Adapter) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.records[a.MAC]; ok {
		a.Name = existing.Name
	}
	m.records[a.MAC] = a
	return nil
}

func (m *MemoryStore) SetName(_ context.Context, mac string, name *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.records[mac]
	if !ok {
		return store.ErrNotFound
	}
	a.Name = name
	m.records[mac] = a
	return nil
}
