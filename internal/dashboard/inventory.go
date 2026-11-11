package dashboard

import (
	"context"

	"services/internal/groups"
)

// projectContainers reconciles completed Compose work before applying update
// comparisons to the monitor's inventory. Workers run in separate containers, so
// completing their journal entry cannot directly invalidate this process's cache.
func (s *Server) projectContainers(ctx context.Context) ([]groups.Container, error) {
	poller, ok := s.source.(interface{ Poll(context.Context) error })
	if !ok {
		return s.source.Containers()
	}
	s.inventoryMu.Lock()
	defer s.inventoryMu.Unlock()

	var completed []Operation
	if s.durableProduction {
		if s.durable != nil {
			var err error
			completed, err = s.durable.List()
			if err != nil {
				return nil, err
			}
		}
	} else {
		q := composeOperations
		q.mu.Lock()
		for _, job := range q.history {
			completed = append(completed, job.operation)
		}
		q.mu.Unlock()
	}

	terminals := make(map[string]string)
	refresh := false
	for _, op := range completed {
		if op.State != "complete" && op.State != "error" {
			continue
		}
		terminals[op.ID] = op.FinishedAt
		if at, seen := s.inventoryCompletions[op.ID]; !seen || at != op.FinishedAt {
			refresh = true
		}
	}
	if refresh {
		// Poll serializes with in-flight background polls and performs a new
		// Docker read. A failed refresh must not bless the retained old snapshot
		// or consume the completion: the next request retries reconciliation.
		if err := poller.Poll(ctx); err != nil {
			return nil, err
		}
	}
	s.inventoryCompletions = terminals
	return s.source.Containers()
}
