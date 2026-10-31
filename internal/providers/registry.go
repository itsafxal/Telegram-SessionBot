package providers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Canonical internal provider IDs
const (
	InternalIDPyrofork = "pyrofork"
	InternalIDTelethon = "telethon"
	InternalIDGogram   = "gogram"
)

// User-facing display names
const (
	DisplayNamePyrogram = "Pyrogram"
	DisplayNameTelethon = "Telethon"
	DisplayNameGogram   = "Gogram"
)

// Factory creates a new isolated Provider instance for an authentication session.
type Factory func(ctx context.Context, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (Provider, error)

// Registry manages available MTProto providers.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

var defaultRegistry = &Registry{
	factories: make(map[string]Factory),
}

// Register registers a provider factory under a given name.
func Register(name string, factory Factory) {
	defaultRegistry.Register(name, factory)
}

// Get creates a new Provider instance by name.
func Get(ctx context.Context, name string, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (Provider, error) {
	return defaultRegistry.Get(ctx, name, apiID, apiHash, pythonPath, timeout)
}

// List returns the names of all registered providers.
func List() []string {
	return defaultRegistry.List()
}

// Register registers a provider factory in this registry.
func (r *Registry) Register(name string, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := NormalizeName(name)
	r.factories[key] = factory
}

// Get instantiates a provider by name from this registry.
func (r *Registry) Get(ctx context.Context, name string, apiID int32, apiHash string, pythonPath string, timeout time.Duration) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := NormalizeName(name)
	factory, exists := r.factories[key]
	if !exists {
		return nil, fmt.Errorf("provider '%s' is not registered", name)
	}

	return factory(ctx, apiID, apiHash, pythonPath, timeout)
}

// List returns all registered canonical provider names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var names []string
	for k := range r.factories {
		names = append(names, k)
	}
	return names
}

// NormalizeName extracts the canonical lowercase provider name, stripping emoji and whitespace.
func NormalizeName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "pyrogram") || strings.Contains(lower, "pyrofork"):
		return InternalIDPyrofork
	case strings.Contains(lower, "telethon"):
		return InternalIDTelethon
	case strings.Contains(lower, "gogram"):
		return InternalIDGogram
	default:
		return strings.TrimSpace(lower)
	}
}

// DisplayName converts any provider ID, alias, or input into the official user-facing display name.
func DisplayName(idOrName string) string {
	canonical := NormalizeName(idOrName)
	switch canonical {
	case InternalIDPyrofork:
		return DisplayNamePyrogram
	case InternalIDTelethon:
		return DisplayNameTelethon
	case InternalIDGogram:
		return DisplayNameGogram
	default:
		return idOrName
	}
}
