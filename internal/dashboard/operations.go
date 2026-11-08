package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"services/internal/operations"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Operation is the shared durable Compose action API.
type Operation = operations.Operation

type composeJob struct {
	operation Operation
	run       func(context.Context, func(string)) error
}

type operationQueue struct {
	mu       sync.Mutex
	busy     bool
	sequence uint64
	history  []*composeJob
	waiting  []*composeJob
}

// All Server instances target the same Docker Engine and share serialization.
var composeOperations = &operationQueue{}

func operationTime() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (q *operationQueue) createLocked(project, action string) *composeJob {
	q.sequence++
	job := &composeJob{operation: Operation{
		ID: fmt.Sprintf("compose-%d", q.sequence), Project: project, Action: action,
		State: "queued", QueuedAt: operationTime(), Messages: []string{},
	}}
	q.history = append(q.history, job)
	return job
}

func (q *operationQueue) snapshotLocked(job *composeJob) Operation {
	op := job.operation
	op.Messages = append([]string{}, op.Messages...)
	for i, candidate := range q.waiting {
		if candidate == job {
			op.QueuePosition = i + 1
			break
		}
	}
	return op
}

const (
	maxQueuedOperations      = 32
	maxCompletedOperations   = 100
	maxOperationMessages     = 200
	maxOperationMessageBytes = 4096
)

func (q *operationQueue) submit(project string, run func(context.Context, func(string)) error) (Operation, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, existing := range q.history {
		if existing.operation.Project == project && existing.operation.Action == "update project" && (existing.operation.State == "queued" || existing.operation.State == "running") {
			return q.snapshotLocked(existing), true
		}
	}
	if len(q.waiting) >= maxQueuedOperations {
		return Operation{}, false
	}
	job := q.createLocked(project, "update project")
	job.run = run
	q.waiting = append(q.waiting, job)
	q.startLocked()
	return q.snapshotLocked(job), true
}

func (q *operationQueue) startLocked() {
	if q.busy || len(q.waiting) == 0 {
		return
	}
	job := q.waiting[0]
	q.waiting[0] = nil
	q.waiting = q.waiting[1:]
	q.busy = true
	job.operation.State = "running"
	job.operation.StartedAt = operationTime()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		err := job.run(ctx, func(message string) { q.message(job, message) })
		q.mu.Lock()
		defer q.mu.Unlock()
		job.operation.State = "complete"
		if err != nil {
			job.operation.State = "error"
			job.operation.Error = boundedOperationMessage(err.Error())
		}
		job.operation.FinishedAt = operationTime()
		job.run = nil
		q.pruneLocked()
		q.busy = false
		q.startLocked()
	}()
}

// beginLegacy is called only while the caller holds the queue reservation.
func (q *operationQueue) beginLegacy(project, action string) *composeJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	job := q.createLocked(project, action)
	job.operation.State = "running"
	job.operation.StartedAt = operationTime()
	return job
}

func (q *operationQueue) finishLegacy(job *composeJob, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job.operation.State = "complete"
	if err != nil {
		job.operation.State = "error"
		job.operation.Error = boundedOperationMessage(err.Error())
	}
	job.operation.FinishedAt = operationTime()
	q.pruneLocked()
}

func (q *operationQueue) message(job *composeJob, message string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job.operation.State != "running" {
		return
	}
	if len(job.operation.Messages) == maxOperationMessages {
		copy(job.operation.Messages, job.operation.Messages[1:])
		job.operation.Messages = job.operation.Messages[:maxOperationMessages-1]
	}
	job.operation.Messages = append(job.operation.Messages, boundedOperationMessage(message))
}

func boundedOperationMessage(message string) string {
	message = strings.ToValidUTF8(message, "")
	if len(message) <= maxOperationMessageBytes {
		return message
	}
	end := maxOperationMessageBytes
	for !utf8.RuneStart(message[end]) {
		end--
	}
	return strings.Clone(message[:end])
}

// Keep all live jobs and the newest bounded terminal history, in submit order.
func (q *operationQueue) pruneLocked() {
	completed := 0
	for _, job := range q.history {
		if job.operation.State == "complete" || job.operation.State == "error" {
			completed++
		}
	}
	kept := q.history[:0]
	for _, job := range q.history {
		terminal := job.operation.State == "complete" || job.operation.State == "error"
		if terminal && completed > maxCompletedOperations {
			completed--
			continue
		}
		kept = append(kept, job)
	}
	for i := len(kept); i < len(q.history); i++ {
		q.history[i] = nil
	}
	q.history = kept
}

// Legacy streams may acquire only an idle queue, never overtake waiting work.
func (s *Server) tryComposeAction(_ string) (func(), bool) {
	q := composeOperations
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.busy || len(q.waiting) != 0 {
		return nil, false
	}
	q.busy = true
	var once sync.Once
	return func() { once.Do(func() { q.mu.Lock(); defer q.mu.Unlock(); q.busy = false; q.startLocked() }) }, true
}

func (s *Server) operations(w http.ResponseWriter, _ *http.Request) {
	if s.durableProduction {
		s.durableOperations(w)
		return
	}
	q := composeOperations
	q.mu.Lock()
	operations := make([]Operation, 0, len(q.history))
	for i := len(q.history) - 1; i >= 0; i-- {
		operations = append(operations, q.snapshotLocked(q.history[i]))
	}
	q.mu.Unlock()
	writeOperationJSON(w, http.StatusOK, struct {
		Operations []Operation `json:"operations"`
	}{operations})
}

func (s *Server) operation(w http.ResponseWriter, r *http.Request) {
	if s.durableProduction {
		s.durableOperation(w, r)
		return
	}
	q := composeOperations
	q.mu.Lock()
	for _, job := range q.history {
		if job.operation.ID == r.PathValue("id") {
			op := q.snapshotLocked(job)
			q.mu.Unlock()
			writeOperationJSON(w, http.StatusOK, op)
			return
		}
	}
	q.mu.Unlock()
	http.Error(w, "Operation not found", http.StatusNotFound)
}

func writeOperationJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
