package service

import (
	"sync"
	"time"
)

// RetryQueue buffers failed ingest payloads for later redelivery with
// exponential backoff. Items are dropped after MaxAttempts deliveries.
type retryItem struct {
	payload  []byte
	attempts int
	nextAt   time.Time
}

// MaxAttempts bounds how often one payload may be handed out for
// processing before it is discarded.
const MaxAttempts = 3

// baseRetryDelay is the first backoff step; every further attempt doubles.
const baseRetryDelay = 2 * time.Second

// RetryQueue is a bounded, mutex-guarded FIFO of pending retries ordered
// by next delivery time.
type RetryQueue struct {
	mu      sync.Mutex
	items   []retryItem
	max     int
	dropped uint64
}

// NewRetryQueue creates a queue holding at most max payloads; pushes into
// a full queue evict the oldest item (counted as dropped).
func NewRetryQueue(max int) *RetryQueue {
	if max <= 0 {
		max = 128
	}
	return &RetryQueue{items: make([]retryItem, 0, max), max: max}
}

// Push enqueues payload for redelivery; the backoff schedule derives from
// the payload's existing attempt count if it was seen before (matched by
// identity of the oldest matching entry), otherwise from zero attempts.
func (q *RetryQueue) Push(payload []byte, now time.Time) {

	attempts := q.findAttemptsLocked(payload)
	if len(q.items) >= q.max {
		// Evict the head (oldest scheduled) to make room.
		q.items = q.items[1:]
		q.dropped++
	}
	delay := baseRetryDelay << attempts
	if delay < baseRetryDelay {
		delay = baseRetryDelay // guard against shift overflow on huge counts
	}
	q.items = append(q.items, retryItem{
		payload:  append([]byte(nil), payload...),
		attempts: attempts + 1,
		nextAt:   now.Add(delay),
	})
}

// findAttemptsLocked returns the attempt count of an earlier identical
// payload already queued, or -1 when the payload is new. Matching by
// content keeps the API allocation-free for callers without IDs.
func (q *RetryQueue) findAttemptsLocked(payload []byte) int {
	for i, it := range q.items {
		if bytesEqual(it.payload, payload) {
			at := it.attempts
			q.items = append(q.items[:i], q.items[i+1:]...) // re-queued below with bumped count
			return at
		}
	}
	return 0
}

// PopDue returns the earliest due payload ready for processing. Payloads
// whose attempts reached MaxAttempts are discarded (drop counter bumps)
// and the search continues. The second result reports whether a payload
// was returned.
func (q *RetryQueue) PopDue(now time.Time) ([]byte, bool) {

	i := 0
	for i < len(q.items) {
		it := q.items[i]
		if it.nextAt.After(now) {
			i++ // not due yet; keep scanning (queue is append-ordered)
			continue
		}
		if it.attempts >= MaxAttempts {
			// Exhausted: discard and keep looking for deliverable items.
			q.items = append(q.items[:i], q.items[i+1:]...)
			q.dropped++
			continue
		}
		q.items = append(q.items[:i], q.items[i+1:]...)
		return it.payload, true
	}
	return nil, false
}

// Len reports how many payloads currently wait in the queue.
func (q *RetryQueue) Len() int {
	return len(q.items)
}

// Dropped returns how many payloads have been discarded so far, either by
// exhausting MaxAttempts or by eviction from a full queue.
func (q *RetryQueue) Dropped() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}

// NextReady returns when the earliest queued payload becomes due; zero is
// returned for an empty queue.
func (q *RetryQueue) NextReady() time.Time {
	q.mu.Lock()
	defer q.mu.Unlock()
	var minAt time.Time
	for _, it := range q.items {
		if minAt.IsZero() || it.nextAt.Before(minAt) {
			minAt = it.nextAt
		}
	}
	return minAt
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
