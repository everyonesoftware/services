package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"services/internal/composeupdate"
	"services/internal/groups"
)

type Source interface {
	Containers() ([]groups.Container, error)
}
type Checker interface {
	CheckNow(context.Context, []groups.Container) error
	Apply([]groups.Container)
}
type VerifiedUpdater interface {
	Pull(context.Context, groups.Container) error
	UpdateProjectVerified(context.Context, []groups.Container, func(string) (string, error), func(groups.Container, string) error) error
}
type Executor struct {
	Store            *Store
	Source           Source
	Checker          Checker
	Updater          VerifiedUpdater
	Engine           *Engine
	Project, Service string
}

func (w *Executor) Execute(parent context.Context, id string) (result error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	defer cancel()
	ok, err := w.Store.BeginExecution(id)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("operation execution already claimed or not running; refusing replay")
	}
	var logMu sync.Mutex
	var logErr error
	phase := "preparing"
	defer func() {
		if recovered := recover(); recovered != nil {
			result = fmt.Errorf("worker panic: %v", recovered)
		}
		logMu.Lock()
		defer logMu.Unlock()
		if logErr != nil {
			result = fmt.Errorf("persist operation progress: %w", logErr)
		}
		attention := result != nil && (phase == "applying" || phase == "verifying" || logErr != nil)
		if err := w.Store.Finish(id, result, attention); err != nil {
			result = fmt.Errorf("persist terminal result: %w", err)
		}
	}()
	o, err := w.Store.Get(id)
	if err != nil {
		return err
	}
	progress := func(line string) {
		logMu.Lock()
		defer logMu.Unlock()
		if strings.HasPrefix(line, "phase: ") {
			phase = strings.TrimPrefix(line, "phase: ")
			if e := w.Store.Change(id, func(v *Operation) { v.Phase = phase }); e != nil {
				logErr = e
				cancel()
			}
		}
		if e := w.Store.Message(id, line); e != nil {
			logErr = e
			cancel()
		}
	}
	ctx = composeupdate.WithProgress(ctx, progress)
	containers, err := w.Source.Containers()
	if err != nil {
		return fmt.Errorf("fresh Docker preflight: %w", err)
	}
	selectedProject := make([]groups.Container, 0, len(containers))
	for _, c := range containers {
		if c.Project == o.Project {
			selectedProject = append(selectedProject, c)
		}
	}
	containers = selectedProject
	if w.Checker == nil {
		return errors.New("fresh registry checking is not configured")
	}
	if err = w.Checker.CheckNow(ctx, containers); err != nil {
		return fmt.Errorf("fresh registry preflight: %w", err)
	}
	w.Checker.Apply(containers)
	members := []groups.Container{}
	var target *groups.Container
	for _, c := range containers {
		if c.Project != o.Project {
			continue
		}
		members = append(members, c)
		if c.ID == o.Target {
			copy := c
			target = &copy
		}
	}
	if len(members) == 0 {
		return errors.New("project no longer exists")
	}
	if o.Action != "update project" {
		if target == nil {
			return errors.New("target container no longer exists")
		}
		if target.State != "running" {
			return errors.New("target container is not running")
		}
		if o.Action == "pull" {
			if !target.UpdateAvailable {
				return errors.New("container has no available update")
			}
			progress("phase: pulling")
			return w.Updater.Pull(ctx, *target)
		}
		if o.Action != "update" {
			return errors.New("unknown operation action")
		}
		// Validate the whole project, but select only the explicitly requested service.
		for i := range members {
			members[i].UpdateAvailable = members[i].Service == target.Service
		}
	}
	self := o.Project == w.Project
	if self && w.Service == "" {
		return errors.New("Services service identity is not configured")
	}
	progress("Starting Compose " + o.Action + " for " + o.Project)
	err = w.Updater.UpdateProjectVerified(ctx, members, func(reference string) (string, error) {
		for _, member := range members {
			if self && member.Service == w.Service && member.UpdateAvailable && member.Image == reference {
				if err := w.Engine.ValidateWorkerImage(ctx, reference); err != nil {
					return "", err
				}
			}
		}
		image, err := w.Engine.ImageID(ctx, reference)
		if err != nil {
			return "", err
		}
		err = w.Store.Change(id, func(operation *Operation) {
			if operation.ExpectedImages == nil {
				operation.ExpectedImages = map[string]string{}
			}
			for _, member := range members {
				if member.UpdateAvailable && member.Image == reference {
					operation.ExpectedImages[member.Service] = image
				}
			}
		})
		return image, err
	}, func(c groups.Container, expected string) error {
		verifyCtx, stop := context.WithTimeout(ctx, 5*time.Minute)
		defer stop()
		return w.Engine.Verify(verifyCtx, c.ID, c.Name, expected, self && c.Service == w.Service)
	})
	if err == nil {
		progress("Compose operation completed successfully")
	}
	return err
}
