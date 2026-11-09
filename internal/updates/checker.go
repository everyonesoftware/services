package updates

import (
	"context"
	"fmt"
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
	publication       *groups.ImagePublication
	publicationID     int64
	digest            string
	checkedAt         time.Time // Cache freshness, including notification receipts.
	registryCheckedAt time.Time // Only successful registry observations.
	retryAt           time.Time
	failed            bool
}

type Checker struct {
	// Lock order is always checker.mu before store I/O. Stores never call checkers.
	publicationStore *PublicationStore
	registry         Registry
	interval         time.Duration
	retryInterval    time.Duration
	mu               sync.Mutex
	results          map[string]cachedResult
	pending          map[string]struct{}
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

// SetPublicationStore enables durable notification reads and acceptance. The caller
// owns the store's lifetime and must close it after the checker has stopped.
func (c *Checker) SetPublicationStore(store *PublicationStore) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.publicationStore = store
	c.results = make(map[string]cachedResult)
}

// AcceptPublication validates and durably records provenance without registry I/O
// or deployment. Duplicate/stale receipts never refresh the original receipt time.
func (c *Checker) AcceptPublication(ctx context.Context, p groups.ImagePublication) (string, error) {
	if err := ValidatePublication(p); err != nil {
		return "", err
	}
	p = copyPublication(p)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.publicationStore == nil {
		return "", fmt.Errorf("publication store unavailable")
	}
	return c.publicationStore.accept(ctx, p)
}

// hydrateLocked reads just the relevant tag so another coordinator's committed
// receipt is visible without a process-local invalidation channel. Publication IDs
// also fence registry requests that started before a notification was accepted.
func (c *Checker) hydrateLocked(ctx context.Context, ref imageReference) error {
	if c.publicationStore == nil || ref.registry != "ghcr.io" {
		return nil
	}
	record, err := c.publicationStore.current(ctx, ref.canonicalRepository(), ref.tag)
	if err != nil {
		return err
	}
	result := c.results[ref.key()]
	if record == nil {
		// Retention may evict this tag in another coordinator. Do not let a
		// process-local notification outlive its durable mapping or suppress
		// ordinary registry discovery for a newly untracked tag.
		if result.publicationID > 0 {
			delete(c.results, ref.key())
		}
		return nil
	}
	if record.ID > result.publicationID {
		p := copyPublication(record.Publication)
		result = cachedResult{digest: p.Digest, checkedAt: record.ReceivedAt, publication: &p, publicationID: record.ID}
	}
	digest, observedAt, err := c.publicationStore.loadObservation(ctx, ref.canonicalRepository(), ref.tag, record.ID)
	if err != nil {
		return err
	}
	if result.publicationID == record.ID && observedAt.After(result.registryCheckedAt) {
		result.digest = digest
		result.checkedAt = observedAt
		result.registryCheckedAt = observedAt
		result.failed = false
		result.retryAt = time.Time{}
		result.publication = nil
		if record.Publication.Digest == digest {
			p := copyPublication(record.Publication)
			result.publication = &p
		}
	}
	c.results[ref.key()] = result
	return nil
}

// Apply attaches cached read-only update state and schedules stale registry checks.
func (c *Checker) Apply(containers []groups.Container) {
	var scheduled []imageReference
	now := time.Now()
	c.mu.Lock()
	for index := range containers {
		container := &containers[index]
		container.UpdateAvailable = false
		container.UpdatePublication = nil
		container.CurrentDigest = ""
		container.ProposedDigest = ""
		container.UpdateStatus = "unavailable"
		container.UpdateError = ""
		container.UpdateCheckedAt = ""
		if container.Project == "" || container.State != "running" || container.MonitorStatus == "missing" {
			continue
		}
		ref, err := parseReference(container.Image)
		if err != nil {
			container.UpdateStatus = "unsupported"
			container.UpdateError = "Only public Docker Hub and GHCR tags can be checked; pinned digests do not move"
			continue
		}
		container.CurrentDigest = localRepositoryDigest(ref, container.RepoDigests)
		key := ref.key()
		if err := c.hydrateLocked(context.Background(), ref); err != nil {
			log.Printf("update monitor could not read publication cache: %v", err)
		}
		result, checked := c.results[key]
		container.UpdateStatus = "checking"
		if checked && !result.registryCheckedAt.IsZero() {
			container.UpdateCheckedAt = result.registryCheckedAt.UTC().Format(time.RFC3339)
		}
		if container.CurrentDigest == "" {
			container.UpdateStatus = "unknown"
			container.UpdateError = "The running image has no matching repository digest"
		}
		if checked && result.failed {
			container.UpdateStatus = "error"
			container.UpdateError = "Registry check failed; authentication or registry availability may be required"
		}
		if checked && !result.failed && result.digest != "" && container.CurrentDigest != "" {
			container.UpdateStatus = "current"
			if result.publication != nil && result.publication.Digest == result.digest {
				p := copyPublication(*result.publication)
				container.UpdatePublication = &p
			}
		}
		if checked && !result.failed && result.digest != "" && container.CurrentDigest != "" {
			container.UpdateAvailable = container.CurrentDigest != result.digest
			if container.UpdateAvailable {
				container.UpdateStatus = "available"
				container.ProposedDigest = result.digest
			}
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

// CheckNow refreshes every distinct running Compose image before returning.
func (c *Checker) CheckNow(ctx context.Context, containers []groups.Container) error {
	seen := make(map[string]struct{})
	var firstErr error
	for _, container := range containers {
		if container.Project == "" || container.State != "running" || container.MonitorStatus == "missing" {
			continue
		}
		ref, err := parseReference(container.Image)
		if err != nil {
			continue
		}
		key := ref.key()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := c.checkNow(ctx, key); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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
	c.mu.Lock()
	if err := c.hydrateLocked(ctx, ref); err != nil {
		c.mu.Unlock()
		return err
	}
	publicationID := c.results[ref.key()].publicationID
	c.mu.Unlock()
	digest, err := c.registry.ManifestDigest(ctx, ref.key())
	c.mu.Lock()
	defer c.mu.Unlock()
	if readErr := c.hydrateLocked(ctx, ref); readErr != nil {
		return readErr
	}
	result := c.results[ref.key()]
	// A receipt accepted during registry I/O wins. A later explicit check starts
	// with that receipt's ID and is allowed to reconcile the registry normally.
	if result.publicationID != publicationID {
		return err
	}
	if err != nil || digest == "" {
		result.failed = true
		if err == nil {
			err = fmt.Errorf("registry returned an empty digest")
		}
		result.retryAt = time.Now().Add(c.retryInterval)
	} else {
		checkedAt := time.Now()
		if publicationID > 0 && c.publicationStore != nil {
			if _, writeErr := c.publicationStore.observe(ctx, ref.canonicalRepository(), ref.tag, publicationID, digest, checkedAt); writeErr != nil {
				return writeErr
			}
			// A different coordinator may have advanced the tag between our
			// preceding read and the conditional write. Re-read its winning state.
			return c.hydrateLocked(ctx, ref)
		}
		result.failed = false
		result.digest = digest
		result.checkedAt = checkedAt
		result.registryCheckedAt = checkedAt
		result.retryAt = time.Time{}
		if result.publication != nil && result.publication.Digest != digest {
			result.publication = nil
		}
	}
	c.results[ref.key()] = result
	return err
}
