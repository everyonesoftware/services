package updates

import (
	"context"
	"log"
	"sync"
	"time"

	"services/internal/groups"
)

const defaultPollInterval = 6 * time.Hour
const defaultFailureRetryInterval = 15 * time.Minute

type Registry interface {
	ManifestDigest(context.Context, string) (string, error)
}

type ContainerSource interface {
	Containers() ([]groups.Container, error)
}

type cachedResult struct {
	digest    string
	checkedAt time.Time
	retryAt   time.Time
}

type Checker struct {
	registry      Registry
	interval      time.Duration
	retryInterval time.Duration
	mu            sync.Mutex
	results       map[string]cachedResult
	pending       map[string]struct{}
}

func NewChecker(registry Registry, interval time.Duration) *Checker {
	return newChecker(registry, interval, defaultFailureRetryInterval)
}

func newChecker(registry Registry, interval, retryInterval time.Duration) *Checker {
	if interval <= 0 {
		interval = defaultPollInterval
	}
	if retryInterval <= 0 || retryInterval > interval {
		retryInterval = defaultFailureRetryInterval
		if retryInterval > interval {
			retryInterval = interval
		}
	}
	return &Checker{
		registry:      registry,
		interval:      interval,
		retryInterval: retryInterval,
		results:       make(map[string]cachedResult),
		pending:       make(map[string]struct{}),
	}
}

// Apply attaches cached read-only update state and schedules stale registry checks.
func (c *Checker) Apply(containers []groups.Container) {
	var scheduled []imageReference
	now := time.Now()
	c.mu.Lock()
	for index := range containers {
		container := &containers[index]
		container.UpdateAvailable = false
		if container.Project == "" || container.State != "running" {
			continue
		}
		ref, err := parseReference(container.Image)
		if err != nil {
			continue
		}
		key := ref.key()
		result, checked := c.results[key]
		if checked && result.digest != "" && hasLocalRepository(ref, container.RepoDigests) {
			container.UpdateAvailable = !hasLocalDigest(ref, container.RepoDigests, result.digest)
		}
		due := !checked || (!result.retryAt.IsZero() && !now.Before(result.retryAt))
		if checked && result.retryAt.IsZero() && result.digest != "" {
			due = now.Sub(result.checkedAt) >= c.interval
		}
		if due && c.registry != nil {
			if _, pending := c.pending[key]; !pending {
				c.pending[key] = struct{}{}
				scheduled = append(scheduled, ref)
			}
		}
	}
	c.mu.Unlock()
	for _, ref := range scheduled {
		go c.refresh(ref)
	}
}

func (c *Checker) Run(ctx context.Context, source ContainerSource) {
	poll := func() {
		containers, err := source.Containers()
		if err != nil {
			log.Printf("update monitor could not read Docker containers: %v", err)
			return
		}
		c.Apply(containers)
	}
	poll()
	ticker := time.NewTicker(c.retryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}

func (c *Checker) refresh(ref imageReference) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := c.checkNow(ctx, ref.key())
	if err != nil {
		log.Printf("update monitor could not check %s: %v", ref.key(), err)
	}
	c.mu.Lock()
	delete(c.pending, ref.key())
	c.mu.Unlock()
}

func (c *Checker) checkNow(ctx context.Context, image string) error {
	ref, err := parseReference(image)
	if err != nil {
		return err
	}
	if c.registry == nil {
		return nil
	}
	digest, err := c.registry.ManifestDigest(ctx, ref.key())
	c.mu.Lock()
	result := c.results[ref.key()]
	if err != nil || digest == "" {
		result.retryAt = time.Now().Add(c.retryInterval)
	} else {
		result.digest = digest
		result.checkedAt = time.Now()
		result.retryAt = time.Time{}
	}
	c.results[ref.key()] = result
	c.mu.Unlock()
	return err
}
