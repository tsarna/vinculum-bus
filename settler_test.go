package bus

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOps stands in for a receiver's protocol verbs, counting what reached
// "the broker" so a test can assert that settle-once meant exactly one call.
type fakeOps struct {
	mu sync.Mutex

	acks       int
	nacks      int
	keepalives int
	reasons    []string

	ackErr       error
	nackErr      error
	keepaliveErr error
	keepaliveOK  bool

	invalidReason string
}

func (o *fakeOps) Ack(context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.acks++
	return o.ackErr
}

func (o *fakeOps) Nack(_ context.Context, reason string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.nacks++
	o.reasons = append(o.reasons, reason)
	return o.nackErr
}

func (o *fakeOps) Keepalive(context.Context) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.keepalives++
	return o.keepaliveOK, o.keepaliveErr
}

func (o *fakeOps) Valid() (bool, string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.invalidReason == "", o.invalidReason
}

func (o *fakeOps) counts() (acks, nacks, keepalives int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.acks, o.nacks, o.keepalives
}

// The whole point of the settle-once rule: two subscribers see the same
// delivery, both take responsibility for it, and the broker hears about it
// once. The one that got there first is told so, and the other is told it did
// not — which is the return value a config can assert on.
func TestFirstSettleWins(t *testing.T) {
	ops := &fakeOps{}
	s := NewSettler(ops)

	first, err := s.Ack(t.Context())
	require.NoError(t, err)
	assert.True(t, first, "the first ack should report that it settled the delivery")

	second, err := s.Ack(t.Context())
	require.NoError(t, err)
	assert.False(t, second, "a second ack should report that it did not settle the delivery")

	acks, _, _ := ops.counts()
	assert.Equal(t, 1, acks, "the broker should have been acked exactly once")
}

// Nack after ack, and ack after nack, are both no-ops rather than errors.
// That is what makes "safe to call from shared handling code" true rather than
// aspirational: neither caller has to know what the other did.
func TestSettleIsOnceWhicheverVerbGoesFirst(t *testing.T) {
	t.Run("nack after ack", func(t *testing.T) {
		ops := &fakeOps{}
		s := NewSettler(ops)

		settled, err := s.Ack(t.Context())
		require.NoError(t, err)
		require.True(t, settled)

		settled, err = s.Nack(t.Context(), "too late")
		require.NoError(t, err)
		assert.False(t, settled)

		acks, nacks, _ := ops.counts()
		assert.Equal(t, 1, acks)
		assert.Zero(t, nacks, "the delivery was already settled, so nothing should have been nacked")
	})

	t.Run("ack after nack", func(t *testing.T) {
		ops := &fakeOps{}
		s := NewSettler(ops)

		settled, err := s.Nack(t.Context(), "gave up")
		require.NoError(t, err)
		require.True(t, settled)

		settled, err = s.Ack(t.Context())
		require.NoError(t, err)
		assert.False(t, settled)

		acks, nacks, _ := ops.counts()
		assert.Zero(t, acks)
		assert.Equal(t, 1, nacks)
		assert.Equal(t, []string{"gave up"}, ops.reasons)
	})
}

// Concurrent settles are the fan-out case with the timing that makes it hard:
// many goroutines, one delivery. Exactly one may claim it and exactly one
// broker call may result.
func TestConcurrentSettlesReachTheBrokerOnce(t *testing.T) {
	ops := &fakeOps{}
	s := NewSettler(ops)

	const callers = 32
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners int
	)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			settled, err := s.Ack(context.Background())
			if err == nil && settled {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, winners, "exactly one caller should have settled the delivery")
	acks, _, _ := ops.counts()
	assert.Equal(t, 1, acks, "the broker should have been acked exactly once")
}

// A failed broker call has settled nothing, so the delivery stays outstanding
// and the next caller is allowed to try. The failing caller is told it did not
// settle it, and gets the reason.
func TestAFailedSettleReleasesTheClaim(t *testing.T) {
	ops := &fakeOps{ackErr: errors.New("XACK failed")}
	s := NewSettler(ops)

	settled, err := s.Ack(t.Context())
	require.Error(t, err)
	assert.False(t, settled)

	ops.mu.Lock()
	ops.ackErr = nil
	ops.mu.Unlock()

	settled, err = s.Ack(t.Context())
	require.NoError(t, err)
	assert.True(t, settled, "the delivery was never settled, so a retry should be able to settle it")

	acks, _, _ := ops.counts()
	assert.Equal(t, 2, acks)
}

// A stale token must not reach the broker at all. On some protocols using one
// does not fail — it settles a different message — so this is the check that
// keeps a late settle from being silent corruption.
func TestAStaleSettlerTouchesNothing(t *testing.T) {
	ops := &fakeOps{invalidReason: "visibility timeout expired"}
	s := NewSettler(ops)

	settled, err := s.Ack(t.Context())
	assert.False(t, settled)
	require.Error(t, err)
	assert.True(t, IsStale(err), "a settle against a stale token should report why")
	assert.Contains(t, err.Error(), "visibility timeout expired")

	settled, err = s.Nack(t.Context(), "whatever")
	assert.False(t, settled)
	assert.True(t, IsStale(err))

	extended, err := s.Keepalive(t.Context())
	assert.False(t, extended)
	assert.True(t, IsStale(err))

	acks, nacks, keepalives := ops.counts()
	assert.Zero(t, acks)
	assert.Zero(t, nacks)
	assert.Zero(t, keepalives)
}

// A token that expires after the delivery was settled is nobody's problem.
// Reporting staleness there would send a reader looking for a timeout that did
// not happen, so settledness is answered first.
func TestAlreadySettledOutranksStale(t *testing.T) {
	ops := &fakeOps{}
	s := NewSettler(ops)

	settled, err := s.Ack(t.Context())
	require.NoError(t, err)
	require.True(t, settled)

	ops.mu.Lock()
	ops.invalidReason = "visibility timeout expired"
	ops.mu.Unlock()

	settled, err = s.Ack(t.Context())
	assert.False(t, settled)
	assert.NoError(t, err, "an already-settled delivery is not a stale one")
}

// Keepalive does not settle: a delivery may be extended any number of times
// while it is still being worked on, and stops being extendable once settled.
func TestKeepaliveDoesNotSettle(t *testing.T) {
	ops := &fakeOps{keepaliveOK: true}
	s := NewSettler(ops)

	for range 3 {
		extended, err := s.Keepalive(t.Context())
		require.NoError(t, err)
		assert.True(t, extended)
	}

	settled, err := s.Ack(t.Context())
	require.NoError(t, err)
	assert.True(t, settled, "keeping a delivery alive must not consume its one settle")

	extended, err := s.Keepalive(t.Context())
	require.NoError(t, err)
	assert.False(t, extended, "there is no lease left to extend once the delivery is settled")

	_, _, keepalives := ops.counts()
	assert.Equal(t, 3, keepalives, "the settled keepalive should not have reached the broker")
}

// A protocol with no per-delivery lease says so, and the caller gets a plain
// false rather than an error — the same answer it gets for a message that
// arrived over a transport with no acknowledgement at all.
func TestKeepaliveWithoutALease(t *testing.T) {
	ops := &fakeOps{keepaliveOK: false}
	s := NewSettler(ops)

	extended, err := s.Keepalive(t.Context())
	require.NoError(t, err)
	assert.False(t, extended)
}

// Most messages did not arrive over a transport that acknowledges, and that is
// not an error: shared handling code calls the settle functions unconditionally
// and they do nothing.
func TestNoSettlerOnABareContext(t *testing.T) {
	assert.Nil(t, SettlerFromContext(context.Background()))
}

func TestSettlerRoundTripsThroughContext(t *testing.T) {
	s := NewSettler(&fakeOps{})
	ctx := WithSettler(context.Background(), s)
	assert.Same(t, s, SettlerFromContext(ctx))
}

// The reason the context is the right channel: an async queue hands the
// delivery to a worker goroutine through context.WithoutCancel, which drops
// cancellation and keeps values. A settle that happens several hops and one
// goroutine later still finds its settler.
func TestSettlerSurvivesWithoutCancel(t *testing.T) {
	ops := &fakeOps{}
	s := NewSettler(ops)

	ctx, cancel := context.WithCancel(WithSettler(context.Background(), s))
	queued := context.WithoutCancel(ctx)
	cancel()

	require.Same(t, s, SettlerFromContext(queued))

	settled, err := SettlerFromContext(queued).Ack(queued)
	require.NoError(t, err)
	assert.True(t, settled)

	acks, _, _ := ops.counts()
	assert.Equal(t, 1, acks)
}
