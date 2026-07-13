// Package notifications implements the review-notifications feature:
// per-session subscriptions to Gerrit changes and a poller that detects
// activity on them and pushes it into the agent's session (see
// docs/glossary.md: Review notifications, Subscription, Channel).
package notifications

import (
	"slices"
	"sync"
	"time"
)

// Store is the per-session subscription set: subscribed change numbers, each
// with a cursor tracking what was already reported. In-memory and trail-free —
// it dies with the process and leaves nothing on the Gerrit instance
// (ADR 2.2); after a restart the agent re-subscribes.
type Store struct {
	mu sync.Mutex `exhaustruct:"optional"`
	// subs maps a subscribed change number to its cursor: the last-seen
	// updated timestamp of the change.
	subs map[int]time.Time
}

// NewStore returns an empty subscription store.
func NewStore() *Store {
	return &Store{subs: make(map[int]time.Time)}
}

// Add registers a subscription with its initial cursor, the change's
// last-known updated timestamp — activity at or before the cursor is never
// reported. Reports false when the change is already subscribed.
func (s *Store) Add(change int, updated time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.subs[change]; ok {
		return false
	}

	s.subs[change] = updated

	return true
}

// Remove ends a subscription. Reports false when the change was not
// subscribed.
func (s *Store) Remove(change int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.subs[change]; !ok {
		return false
	}

	delete(s.subs, change)

	return true
}

// Changes reports the subscribed change numbers in ascending order.
func (s *Store) Changes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()

	changes := make([]int, 0, len(s.subs))
	for n := range s.subs {
		changes = append(changes, n)
	}

	slices.Sort(changes)

	return changes
}

// Advance moves a change's cursor to updated when updated is newer and
// reports whether it moved. The compare and the move are one atomic step, and
// an unsubscribed change never advances — a poll result racing an
// unsubscribe cannot resurrect the subscription.
func (s *Store) Advance(change int, updated time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	cursor, ok := s.subs[change]
	if !ok || !updated.After(cursor) {
		return false
	}

	s.subs[change] = updated

	return true
}
