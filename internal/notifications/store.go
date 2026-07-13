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

// Cursor tracks what was already reported for one subscribed change:
// per-kind high-water marks plus the last status the session saw. Every
// activity kind is reported at most once because its mark only moves
// forward.
type Cursor struct {
	// Updated is the change's updated timestamp as of the last processed
	// tick; it gates the cheap movement check before any detail fetch.
	Updated time.Time
	// Messages is the newest change-message date already reported.
	Messages time.Time
	// Votes is the newest vote date already reported.
	Votes time.Time
	// Comments is the newest comment update already reported.
	Comments time.Time
	// Status is the change status as of the last report; a mismatch on the
	// next delta is a status transition.
	Status string
}

// NewCursor seeds every mark from the change's updated timestamp: at
// subscribe time everything the change already carries is old news, and
// anything that happens later is dated after it.
func NewCursor(updated time.Time, status string) Cursor {
	return Cursor{
		Updated:  updated,
		Messages: updated,
		Votes:    updated,
		Comments: updated,
		Status:   status,
	}
}

// Store is the per-session subscription set: subscribed change numbers, each
// with its cursor. In-memory and trail-free — it dies with the process and
// leaves nothing on the Gerrit instance (ADR 2.2); after a restart the agent
// re-subscribes.
type Store struct {
	mu   sync.Mutex `exhaustruct:"optional"`
	subs map[int]Cursor
}

// NewStore returns an empty subscription store.
func NewStore() *Store {
	return &Store{subs: make(map[int]Cursor)}
}

// Add registers a subscription with its initial cursor. Reports false when
// the change is already subscribed.
func (s *Store) Add(change int, cur Cursor) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.subs[change]; ok {
		return false
	}

	s.subs[change] = cur

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

// Cursor reports a change's cursor; ok is false when the change is not
// subscribed.
func (s *Store) Cursor(change int) (Cursor, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.subs[change]

	return cur, ok
}

// SetCursor commits a processed cursor. It reports false without storing
// anything when the change is no longer subscribed, so a poll result racing
// an unsubscribe cannot resurrect the subscription.
func (s *Store) SetCursor(change int, cur Cursor) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.subs[change]; !ok {
		return false
	}

	s.subs[change] = cur

	return true
}
