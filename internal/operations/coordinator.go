package operations

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

type Workers interface {
	Inspect(context.Context, string) (Inspect, error)
	Launch(context.Context, WorkerConfig, Operation) error
	Start(context.Context, string) error
	Remove(context.Context, string) error
}
type Coordinator struct {
	Store   *Store
	Workers Workers
	Config  WorkerConfig
}

func (c *Coordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		call, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := c.Tick(call)
		cancel()
		if err != nil {
			log.Printf("operation reconciliation: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (c *Coordinator) Tick(ctx context.Context) error {
	unlock, err := c.Store.lockCoordinator(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	ops, err := c.Store.List()
	if err != nil {
		return err
	}
	active := false
	for _, o := range ops {
		if o.Worker == "" || o.State == "queued" {
			continue
		}
		inspected, err := c.Workers.Inspect(ctx, o.Worker)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				return err
			}
			if o.State == "running" {
				if err = c.Store.Finish(o.ID, errors.New("operation interrupted: helper is missing; outcome requires operator review, not automatic retry"), true); err != nil {
					return err
				}
			} else {
				if err = c.Store.Change(o.ID, func(v *Operation) { v.Worker = "" }); err != nil {
					return err
				}
			}
			continue
		}
		if inspected.Config.Labels["io.services.worker"] != "true" || inspected.Config.Labels["io.services.operation"] != o.ID {
			return fmt.Errorf("helper ownership mismatch for %s; refusing action", o.Worker)
		}
		if o.State != "running" {
			if inspected.State.Running {
				active = true
				continue
			}
			if err = c.Workers.Remove(ctx, o.Worker); err != nil {
				return err
			}
			if err = c.Store.Change(o.ID, func(v *Operation) { v.Worker = "" }); err != nil {
				return err
			}
			continue
		}
		active = true
		switch inspected.State.Status {
		case "created":
			if err = c.Workers.Start(ctx, o.Worker); err != nil {
				return err
			}
		case "running", "restarting", "paused": // Never use a heartbeat to authorize replacement.
		default:
			if err = c.Store.Finish(o.ID, fmt.Errorf("operation interrupted: helper exited (%d) without a durable terminal result; review outcome", inspected.State.ExitCode), true); err != nil {
				return err
			}
		}
	}
	if active {
		return nil
	}
	o, err := c.Store.Claim()
	if err != nil || o == nil {
		return err
	}
	if err = c.Store.Change(o.ID, func(v *Operation) { v.SelfUpdate = v.Project == c.Config.Project && v.Action != "pull" }); err != nil {
		return err
	}
	// Claim is persisted before create. An ambiguous create/start is reconciled by
	// deterministic name, never repeated with a second helper or a second command.
	if err = c.Workers.Launch(ctx, c.Config, *o); err != nil {
		_ = c.Store.Change(o.ID, func(v *Operation) { v.Phase = "recovering" })
		return err
	}
	return nil
}
