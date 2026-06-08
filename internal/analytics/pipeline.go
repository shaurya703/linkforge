// Package analytics records click events asynchronously so the redirect hot path
// never blocks on a database write. The redirect handler does a non-blocking
// hand-off to a buffered channel; a pool of workers batches events and flushes
// them to Postgres. If the buffer is full we drop (and count) the event rather
// than slow down redirects — analytics is best-effort by design.
package analytics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/shaurya703/linkforge/internal/domain"
)

// Store is the persistence the pipeline needs (satisfied by the repository).
type Store interface {
	InsertClicks(ctx context.Context, events []domain.ClickEvent) error
	IncrementClickCounts(ctx context.Context, counts map[string]int64) error
}

type counter interface{ Inc() }
type gauge interface{ Set(float64) }

// Pipeline is the async click-processing pipeline.
type Pipeline struct {
	ch            chan domain.ClickEvent
	store         Store
	log           *slog.Logger
	workers       int
	batchSize     int
	flushInterval time.Duration

	dropped counter
	queued  gauge

	wg   sync.WaitGroup
	once sync.Once
}

// Config configures the pipeline.
type Config struct {
	Buffer        int
	Workers       int
	BatchSize     int
	FlushInterval time.Duration
}

// New creates a pipeline. dropped/queued are Prometheus collectors.
func New(store Store, cfg Config, log *slog.Logger, dropped counter, queued gauge) *Pipeline {
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	return &Pipeline{
		ch:            make(chan domain.ClickEvent, cfg.Buffer),
		store:         store,
		log:           log,
		workers:       cfg.Workers,
		batchSize:     cfg.BatchSize,
		flushInterval: cfg.FlushInterval,
		dropped:       dropped,
		queued:        queued,
	}
}

// Start launches the worker pool.
func (p *Pipeline) Start() {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
}

// Enqueue hands a click event to the pipeline without blocking. If the buffer is
// full the event is dropped and counted, protecting redirect latency.
func (p *Pipeline) Enqueue(e domain.ClickEvent) {
	select {
	case p.ch <- e:
		if p.queued != nil {
			p.queued.Set(float64(len(p.ch)))
		}
	default:
		if p.dropped != nil {
			p.dropped.Inc()
		}
	}
}

// Stop closes the input channel and waits for workers to drain and flush all
// buffered events. Safe to call once.
func (p *Pipeline) Stop() {
	p.once.Do(func() { close(p.ch) })
	p.wg.Wait()
}

func (p *Pipeline) worker() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.flushInterval)
	defer ticker.Stop()

	batch := make([]domain.ClickEvent, 0, p.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		p.flush(batch)
		batch = batch[:0]
	}

	for {
		select {
		case e, ok := <-p.ch:
			if !ok {
				flush() // channel closed: final drain
				return
			}
			batch = append(batch, e)
			if len(batch) >= p.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// flush persists a batch: detail rows via COPY, plus aggregated per-code counters.
// Uses a fresh, bounded context so a batch still completes during shutdown.
func (p *Pipeline) flush(batch []domain.ClickEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := p.store.InsertClicks(ctx, batch); err != nil {
		p.log.Error("analytics: insert clicks failed", "n", len(batch), "err", err)
		// Don't double-count on a failed detail insert; skip the counter update.
		return
	}
	counts := make(map[string]int64, len(batch))
	for _, e := range batch {
		counts[e.Code]++
	}
	if err := p.store.IncrementClickCounts(ctx, counts); err != nil {
		p.log.Error("analytics: increment counts failed", "err", err)
	}
}
