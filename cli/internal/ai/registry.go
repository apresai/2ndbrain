package ai

import (
	"context"
	"fmt"
	"sync"
)

// Registry holds registered AI providers.
type Registry struct {
	mu          sync.RWMutex
	embedders   map[string]EmbeddingProvider
	generators  map[string]GenerationProvider
	rerankers   map[string]RerankProvider
	unavailable map[string]error
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{
		embedders:   make(map[string]EmbeddingProvider),
		generators:  make(map[string]GenerationProvider),
		rerankers:   make(map[string]RerankProvider),
		unavailable: make(map[string]error),
	}
}

// NoteUnavailable records why a provider could not register, so later
// Embedder/Generator/Reranker lookups wrap the cause instead of returning a
// bare "not registered". InitBedrock uses this for an *UnroutedSlotError.
// Register* clears the note: the MCP server is long-lived and a stale cause
// must not outlive the config fix that resolved it.
func (r *Registry) NoteUnavailable(name string, err error) {
	if r == nil || name == "" || err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.unavailable == nil {
		r.unavailable = make(map[string]error)
	}
	r.unavailable[name] = err
}

func (r *Registry) clearUnavailableLocked(name string) {
	delete(r.unavailable, name)
}

func (r *Registry) missingLocked(kind, name string) error {
	if err := r.unavailable[name]; err != nil {
		return fmt.Errorf("%s provider %q not registered: %w", kind, name, err)
	}
	return fmt.Errorf("%s provider %q not registered", kind, name)
}

// RegisterEmbedder adds an embedding provider.
func (r *Registry) RegisterEmbedder(name string, p EmbeddingProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.embedders[name] = p
	r.clearUnavailableLocked(name)
}

// RegisterGenerator adds a generation provider.
func (r *Registry) RegisterGenerator(name string, p GenerationProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.generators[name] = p
	r.clearUnavailableLocked(name)
}

// RegisterReranker adds a rerank provider.
func (r *Registry) RegisterReranker(name string, p RerankProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rerankers[name] = p
	r.clearUnavailableLocked(name)
}

// Reranker returns the named rerank provider.
func (r *Registry) Reranker(name string) (RerankProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.rerankers[name]
	if !ok {
		return nil, r.missingLocked("rerank", name)
	}
	return p, nil
}

// Embedder returns the named embedding provider.
func (r *Registry) Embedder(name string) (EmbeddingProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.embedders[name]
	if !ok {
		return nil, r.missingLocked("embedding", name)
	}
	return p, nil
}

// Generator returns the named generation provider.
func (r *Registry) Generator(name string) (GenerationProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.generators[name]
	if !ok {
		return nil, r.missingLocked("generation", name)
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

// ListModels aggregates ModelInfo from all registered providers, deduplicating by (provider, id).
func (r *Registry) ListModels(ctx context.Context) []ModelInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]bool)
	var models []ModelInfo
	add := func(ms []ModelInfo) {
		for _, m := range ms {
			key := m.Provider + "\x00" + m.ID
			if !seen[key] {
				seen[key] = true
				models = append(models, m)
			}
		}
	}
	for _, p := range r.embedders {
		if ms, err := p.ListModels(ctx); err == nil {
			add(ms)
		}
	}
	for _, p := range r.generators {
		if ms, err := p.ListModels(ctx); err == nil {
			add(ms)
		}
	}
	return models
}

// DefaultRegistry is the global provider registry.
var DefaultRegistry = NewRegistry()
