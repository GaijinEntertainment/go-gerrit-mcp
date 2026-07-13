package notifications_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/notifications"
)

func Test_NewCursor(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	cur := notifications.NewCursor(base, "NEW")

	assert.Equal(t, base, cur.Updated)
	assert.Equal(t, base, cur.Messages)
	assert.Equal(t, base, cur.Votes)
	assert.Equal(t, base, cur.Comments)
	assert.Equal(t, "NEW", cur.Status)
}

func Test_Store(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	seed := notifications.NewCursor(base, "NEW")

	t.Run("add registers a subscription", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()

		assert.True(t, s.Add(123, seed))
		assert.Equal(t, []int{123}, s.Changes())

		cur, ok := s.Cursor(123)
		require.True(t, ok)
		assert.Equal(t, seed, cur)
	})

	t.Run("duplicate add refused", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()

		assert.True(t, s.Add(123, seed))
		assert.False(t, s.Add(123, notifications.NewCursor(base.Add(time.Hour), "MERGED")))

		cur, ok := s.Cursor(123)
		require.True(t, ok)
		assert.Equal(t, seed, cur, "refused add must not touch the stored cursor")
	})

	t.Run("remove ends a subscription", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()

		assert.True(t, s.Add(123, seed))
		assert.True(t, s.Remove(123))
		assert.Empty(t, s.Changes())

		_, ok := s.Cursor(123)
		assert.False(t, ok)
	})

	t.Run("remove of an unsubscribed change reports false", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()

		assert.False(t, s.Remove(123))
	})

	t.Run("changes sorted ascending", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()

		s.Add(456, seed)
		s.Add(123, seed)
		s.Add(789, seed)

		assert.Equal(t, []int{123, 456, 789}, s.Changes())
	})

	t.Run("set cursor commits for a subscribed change", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()
		s.Add(123, seed)

		next := notifications.NewCursor(base.Add(time.Minute), "MERGED")

		assert.True(t, s.SetCursor(123, next))

		cur, ok := s.Cursor(123)
		require.True(t, ok)
		assert.Equal(t, next, cur)
	})

	t.Run("set cursor on an unsubscribed change is refused", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()

		assert.False(t, s.SetCursor(123, seed))

		_, ok := s.Cursor(123)
		assert.False(t, ok, "refused commit must not resurrect the subscription")
	})
}

// Test_Store_Concurrent exercises every store operation from concurrent
// goroutines; the -race run is the assertion.
func Test_Store_Concurrent(t *testing.T) {
	t.Parallel()

	const workers = 8

	s := notifications.NewStore()
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup

	for w := range workers {
		wg.Go(func() {
			for i := range 100 {
				change := w*1000 + i

				s.Add(change, notifications.NewCursor(base, "NEW"))
				s.Cursor(change)
				s.SetCursor(change, notifications.NewCursor(base.Add(time.Duration(i)*time.Second), "NEW"))
				s.Changes()
				s.Remove(change)
			}
		})
	}

	wg.Wait()

	assert.Empty(t, s.Changes())
}
