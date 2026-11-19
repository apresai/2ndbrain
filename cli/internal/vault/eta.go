package vault

import (
	"math"
	"sync"

	"github.com/apresai/2ndbrain/internal/store"
)

// BaselineEmbedThroughput returns conservative initial throughput priors for a
// provider before any historical builds exist in metrics.db.
func BaselineEmbedThroughput(provider string, concurrency int) (docsPerSec, charsPerSec, warmupSec float64) {
	switch provider {
	case "llama-local":
		docsPerSec, charsPerSec, warmupSec = 12.0, 18000.0, 0.6
	case "ollama":
		docsPerSec, charsPerSec, warmupSec = 6.0, 9000.0, 0.4
	case "gemini":
		docsPerSec, charsPerSec, warmupSec = 6.0, 16000.0, 0.3
		if concurrency > 0 && concurrency != 4 {
			scale := math.Max(0.5, math.Min(2.0, float64(concurrency)/4.0))
			docsPerSec *= scale
			charsPerSec *= scale
		}
	default: // bedrock, openrouter, etc.
		docsPerSec, charsPerSec, warmupSec = 4.5, 12000.0, 0.3
		if concurrency > 0 && concurrency != 4 {
			scale := math.Max(0.5, math.Min(2.0, float64(concurrency)/4.0))
			docsPerSec *= scale
			charsPerSec *= scale
		}
	}
	return docsPerSec, charsPerSec, warmupSec
}

// EstimateInitialEmbedSeconds computes a pre-run duration estimate in seconds
// for embedding embeddableDocs totaling totalChars characters. Prefers measured
// historical throughput for the active model when available, otherwise falls
// back to calibrated per-provider baselines. Returns (0, "none") if there are
// no embeddable documents.
func EstimateInitialEmbedSeconds(embeddableDocs, totalChars int, provider string, concurrency int, histDocsPerSec, histCharsPerSec float64) (estSec float64, source string) {
	if embeddableDocs <= 0 {
		return 0, "none"
	}
	if histDocsPerSec > 0 && histCharsPerSec > 0 {
		docTime := float64(embeddableDocs) / histDocsPerSec
		charTime := float64(totalChars) / histCharsPerSec
		return math.Max(docTime, charTime), "history"
	}
	docsPerSec, charsPerSec, warmupSec := BaselineEmbedThroughput(provider, concurrency)
	docTime := float64(embeddableDocs) / docsPerSec
	charTime := float64(totalChars) / charsPerSec
	return math.Max(docTime, charTime) + warmupSec, "baseline"
}

// EmbedETASnapshot captures a point-in-time progress, throughput, and ETA
// reading from EmbedETATracker.
type EmbedETASnapshot struct {
	Processed       int
	DoneEmbeddable  int
	EmbeddableTotal int
	DoneChunks      int
	TotalChunks     int
	DoneChars       int
	TotalChars      int
	DocsPerSec      float64
	ChunksPerSec    float64
	ETASeconds      float64
	ElapsedMs       int64
}

// EmbedETATracker tracks work-weighted progress across concurrent workers
// during an embedding pass so small notes do not overly skew ETA on vaults with
// uneven document sizes.
type EmbedETATracker struct {
	mu              sync.Mutex
	docSizing       map[string]store.DocEmbedSizing
	embeddableDocs  int
	totalChunks     int
	totalChars      int
	initialEstSec   float64
	processed       int
	doneEmbeddable  int
	doneChunks      int
	doneChars       int
	completedWeight float64
}

// NewEmbedETATracker builds a tracker for a work set sized by docSizing.
func NewEmbedETATracker(docSizing map[string]store.DocEmbedSizing, embeddableDocs, totalChunks, totalChars int, initialEstSec float64) *EmbedETATracker {
	return &EmbedETATracker{
		docSizing:      docSizing,
		embeddableDocs: embeddableDocs,
		totalChunks:    totalChunks,
		totalChars:     totalChars,
		initialEstSec:  initialEstSec,
	}
}

// Observe records completion of one document in the work set and returns the
// updated work-weighted ETA snapshot.
func (t *EmbedETATracker) Observe(docID string, elapsedMs int64) EmbedETASnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.processed++
	if s, ok := t.docSizing[docID]; ok && s.Chunks > 0 && t.embeddableDocs > 0 {
		t.doneEmbeddable++
		t.doneChunks += s.Chunks
		t.doneChars += s.Chars

		var w float64
		docShare := 1.0 / float64(t.embeddableDocs)
		if t.totalChars > 0 {
			charShare := float64(s.Chars) / float64(t.totalChars)
			w = 0.5*docShare + 0.5*charShare
		} else {
			w = docShare
		}
		t.completedWeight = math.Min(1.0, t.completedWeight+w)
	}

	var docsPerSec, chunksPerSec, etaSec float64
	elapsedSec := float64(elapsedMs) / 1000.0
	if elapsedSec > 0 && t.doneEmbeddable > 0 {
		docsPerSec = float64(t.doneEmbeddable) / elapsedSec
		chunksPerSec = float64(t.doneChunks) / elapsedSec

		if t.completedWeight > 0 {
			liveWorkPerSec := t.completedWeight / elapsedSec
			effectiveWorkPerSec := liveWorkPerSec
			if t.initialEstSec > 0 && t.doneEmbeddable < 4 {
				alpha := float64(t.doneEmbeddable) / 4.0
				effectiveWorkPerSec = (1.0-alpha)*(1.0/t.initialEstSec) + alpha*liveWorkPerSec
			}
			if effectiveWorkPerSec > 0 {
				etaSec = math.Max(0, (1.0-t.completedWeight)/effectiveWorkPerSec)
			}
		}
	} else if t.initialEstSec > 0 {
		remainingFraction := math.Max(0, 1.0-t.completedWeight)
		etaSec = t.initialEstSec * remainingFraction
	}

	return EmbedETASnapshot{
		Processed:       t.processed,
		DoneEmbeddable:  t.doneEmbeddable,
		EmbeddableTotal: t.embeddableDocs,
		DoneChunks:      t.doneChunks,
		TotalChunks:     t.totalChunks,
		DoneChars:       t.doneChars,
		TotalChars:      t.totalChars,
		DocsPerSec:      docsPerSec,
		ChunksPerSec:    chunksPerSec,
		ETASeconds:      etaSec,
		ElapsedMs:       elapsedMs,
	}
}
