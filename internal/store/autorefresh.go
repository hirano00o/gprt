package store

import (
	"context"
	"time"
)

// StartAutoRefresh starts a background ticker that calls Refresh every
// interval, until ctx is done. Per docs/DESIGN.md's concurrency rules, the
// ticker goroutine's only action is to dispatch: Refresh itself, like every
// other Store method, always runs on the UI goroutine.
func (s *Store) StartAutoRefresh(ctx context.Context, interval time.Duration) {
	go func() {
		defer s.recoverGoroutine("auto-refresh")

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.deps.Dispatch(func() { s.Refresh() })
			}
		}
	}()
}
