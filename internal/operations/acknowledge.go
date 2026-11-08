package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrNotInterrupted = errors.New("only interrupted terminal operations can be acknowledged")

// Acknowledge releases an operator-reviewed interruption without changing its
// failed outcome or replaying the action. Docker must prove its helper cannot
// still be executing; connectivity failures are not evidence of absence.
func (c *Coordinator) Acknowledge(ctx context.Context, id string) error {
	o, err := c.Store.Get(id)
	if err != nil {
		return err
	}
	if o.State != "error" || o.Phase != "interrupted" {
		return ErrNotInterrupted
	}
	if !o.NeedsAttention {
		return nil
	}
	if o.Worker != "" {
		inspected, err := c.Workers.Inspect(ctx, o.Worker)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil {
			if inspected.Config.Labels["io.services.worker"] != "true" || inspected.Config.Labels["io.services.operation"] != id {
				return fmt.Errorf("helper ownership mismatch; refusing acknowledgement")
			}
			if inspected.State.Running || (inspected.State.Status != "exited" && inspected.State.Status != "dead") {
				return fmt.Errorf("helper is still active; cannot release the queue")
			}
		}
	}
	return c.Store.transaction(func(tx *sql.Tx) error {
		var body string
		if err := tx.QueryRow(`SELECT body FROM operations WHERE id=?`, id).Scan(&body); err != nil {
			return err
		}
		current, err := decode(body)
		if err != nil {
			return err
		}
		if current.State != "error" || current.Phase != "interrupted" {
			return ErrNotInterrupted
		}
		// Worker assignment cannot change while an interrupted result holds the queue.
		// A coordinator may clear the identity after observing exit/removing the helper.
		if current.Worker != "" && current.Worker != o.Worker {
			return fmt.Errorf("helper identity changed; retry acknowledgement")
		}
		if !current.NeedsAttention {
			return nil
		}
		current.NeedsAttention = false
		current.Messages = append(current.Messages, "Operator acknowledged the uncertain outcome; queued work may resume. This operation was not retried.")
		if len(current.Messages) > MaxMessages {
			current.Messages = current.Messages[len(current.Messages)-MaxMessages:]
		}
		return save(tx, current)
	})
}
