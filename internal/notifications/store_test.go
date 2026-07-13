package notifications_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/notifications"
)

func Test_Store(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	t.Run("add registers a subscription", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()

		assert.True(t, s.Add(123, base))
		assert.Equal(t, []int{123}, s.Changes())
	})

	t.Run("duplicate add refused", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()

		assert.True(t, s.Add(123, base))
		assert.False(t, s.Add(123, base.Add(time.Hour)))
	})

	t.Run("remove ends a subscription", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()

		assert.True(t, s.Add(123, base))
		assert.True(t, s.Remove(123))
		assert.Empty(t, s.Changes())
	})

	t.Run("remove of an unsubscribed change reports false", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()

		assert.False(t, s.Remove(123))
	})

	t.Run("changes sorted ascending", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()

		s.Add(456, base)
		s.Add(123, base)
		s.Add(789, base)

		assert.Equal(t, []int{123, 456, 789}, s.Changes())
	})

	t.Run("advance moves the cursor once per movement", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()
		s.Add(123, base)

		moved := base.Add(time.Minute)

		assert.True(t, s.Advance(123, moved), "newer updated must advance")
		assert.False(t, s.Advance(123, moved), "same updated must not re-advance")
		assert.False(t, s.Advance(123, base), "older updated must not advance")
	})

	t.Run("advance on an unsubscribed change reports false", func(t *testing.T) {
		t.Parallel()

		s := notifications.NewStore()

		assert.False(t, s.Advance(123, base))
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

				s.Add(change, base)
				s.Advance(change, base.Add(time.Duration(i)*time.Second))
				s.Changes()
				s.Remove(change)
			}
		})
	}

	wg.Wait()

	assert.Empty(t, s.Changes())
}
