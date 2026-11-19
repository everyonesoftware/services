package updates

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"services/internal/groups"
)

// PublicationEventReceivedAt returns the original durable acceptance time for an
// exact retained payload. PublishedAt is untrusted metadata; duplicate deliveries
// must not acquire fresh receipt evidence after identity reconciliation. Missing
// or pruned evidence returns zero time and cannot certify publication delivery.
func (s *PublicationStore) PublicationEventReceivedAt(ctx context.Context, p groups.ImagePublication) (time.Time, error) {
	if err := ValidatePublication(p); err != nil {
		return time.Time{}, err
	}
	p = copyPublication(p)
	sort.Strings(p.Tags)
	canonical, err := json.Marshal(p)
	if err != nil {
		return time.Time{}, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT payload,received_at FROM publication_events WHERE repository=? AND run_id=? AND attempt=? LIMIT 2", p.Repository, p.RunID, p.RunAttempt)
	if err != nil {
		return time.Time{}, err
	}
	defer rows.Close()
	var received time.Time
	count := 0
	for rows.Next() {
		count++
		var payload, stamp string
		if err := rows.Scan(&payload, &stamp); err != nil {
			return time.Time{}, err
		}
		if count > 1 || payload != string(canonical) {
			return time.Time{}, ErrPublicationConflict
		}
		received, err = time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return time.Time{}, err
		}
	}
	return received, rows.Err()
}

// PublicationEventReceivedAt preserves the store's original acceptance evidence.
func (c *Checker) PublicationEventReceivedAt(ctx context.Context, p groups.ImagePublication) (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.publicationStore == nil {
		return time.Time{}, errors.New("publication store unavailable")
	}
	return c.publicationStore.PublicationEventReceivedAt(ctx, p)
}
