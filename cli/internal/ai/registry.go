package ai

import (
	"context"
	"fmt"
	"sync"
)

// Registry holds registered AI providers.
type Registry struct {
	mu         sync.RWMutex
	embedders  map[string]EmbeddingProvider
	generators map[string]GenerationProvider
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{
		embedders:  make(map[string]EmbeddingProvider),
		generators: make(map[string]GenerationProvider),
	}
}

// RegisterEmbedder adds an embedding provider.
func (r *Registry) RegisterEmbedder(name string, p EmbeddingProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.embedders[name] = p
}

// RegisterGenerator adds a generation provider.
func (r *Registry) RegisterGenerator(name string, p GenerationProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.generators[name] = p
}

// Embedder returns the named embedding provider.
func (r *Registry) Embedder(name string) (EmbeddingProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.embedders[name]
	if !ok {
		return nil, fmt.Errorf("embedding provider %q not registered", name)
	}
	return p, nil
}

// Generator returns the named generation provider.
func (r *Registry) Generator(name string) (GenerationProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.generators[name]
	if !ok {
		return nil, fmt.Errorf("generation provider %q not registered", name)
	}
	return p, nil
}

// EmbedderNames returns all registered embedding provider names.
func (r *Registry) EmbedderNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.embedders))
	for name := range r.embedders {
		names = append(names, name)
	}
	return names
}

// GeneratorNames returns all registered generation provider names.
func (r *Registry) GeneratorNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.generators))
	for name := range r.generators {
		names = append(names, name)
	}
	return names
}

// ListModels aggregates ModelInfo from all registered providers.
func (r *Registry) ListModels(ctx context.Context) []ModelInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var models []ModelInfo
	for _, p := range r.embedders {
		if ms, err := p.ListModels(ctx); err == nil {
			models = append(models, ms...)
		}
	}
	for _, p := range r.generators {
		if ms, err := p.ListModels(ctx); err == nil {
			models = append(models, ms...)
		}
	}
	return models
}

// DefaultRegistry is the global provider registry.
var DefaultRegistry = NewRegistry()
