package storage

import (
	"context"
	"log/slog"
	"time"
)

// Pruner is satisfied by any store that supports retention-based cleanup.
// SampleStore embeds it directly; postgres.EventStore also implements it
// (as a concrete extra method, not part of the EventStore interface above,
// since deploy events aren't part of the agreed-upon interface contract) so
// both can share RunPruneLoop below.
type Pruner interface {
	Prune(ctx context.Context, olderThan time.Time) error
}

// RunPruneLoop periodically deletes records older than retention from p,
// until ctx is cancelled. name is used only for logging, to distinguish
// which store a given sweep belongs to.
//
// This is the mechanism backing --state-retention / --state-prune-interval
// in cmd/operator: it is deliberately generic (works against any Pruner)
// rather than duplicated per store type.
func RunPruneLoop(ctx context.Context, name string, p Pruner, interval, retention time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	if retention <= 0 {
		// A zero/negative retention would prune everything on every tick,
		// which is almost certainly a misconfiguration rather than intent.
		logger.Warn("storage: retention <= 0, disabling prune loop", "store", name)
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logger.Info("storage: prune loop started", "store", name, "interval", interval, "retention", retention)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().UTC().Add(-retention)
			if err := p.Prune(ctx, cutoff); err != nil {
				logger.Error("storage: prune failed", "store", name, "err", err)
				continue
			}
			logger.Debug("storage: prune swept", "store", name, "cutoff", cutoff)
		}
	}
}
