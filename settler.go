package bus

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

// Acknowledgement is a property of an inbound delivery — of the message that
// arrived from a broker — and not of the payload, nor of whichever subscriber
// happens to handle it. A bus is publish/subscribe and a broker acknowledgement
// is point-to-point, so the two cannot be carried by the same channel: `fields`
// is rewritten per subscription with that subscription's own topic captures,
// and there is no per-message metadata channel that survives a bus hop.
//
// The Go context is that channel. It already crosses every hop that matters,
// the async queue explicitly preserves its values across the goroutine boundary
// (context.WithoutCancel), and it is where a delivery's other cross-cutting
// facts already live. So a receiver puts a Settler on the context it delivers
// with, and anything downstream can settle the delivery without knowing which
// protocol produced it.
//
// The bus itself never settles anything and never inspects a Settler. Settling
// is the receiver's contract with its broker; the bus knows nothing about
// brokers and must not learn. What lives here is the vocabulary the receivers
// and their consumers need in common, in the one module all of them already
// depend on.

// Settler settles one inbound delivery. Implementations are supplied by the
// receiver that produced the message and are safe for concurrent use.
//
// A delivery settles exactly once. The first Ack or Nack wins; every later
// call is a no-op reporting false, which is what makes these safe to call from
// shared handling code that does not know how many other subscribers saw the
// same message. An acknowledgement means "someone took responsibility", not
// "everyone finished" — the set of matching subscribers changes at runtime, and
// a subscription that only logs must not gate a broker acknowledgement.
type Settler interface {
	// Ack settles the delivery as handled. It reports whether this call was
	// the one that settled it.
	Ack(ctx context.Context) (bool, error)

	// Nack settles the delivery as not handled. reason is advisory: it becomes
	// a dead-letter header, a log field, or is discarded, per protocol.
	// Whether the message is requeued or dead-lettered is the receiver's
	// configured policy, never the caller's choice.
	Nack(ctx context.Context, reason string) (bool, error)

	// Keepalive extends the delivery's lease, where the protocol has one. It
	// reports whether anything was extended, and does not settle: a delivery
	// may be kept alive any number of times, and not at all once settled.
	Keepalive(ctx context.Context) (bool, error)
}

// SettleOps are one receiver's protocol verbs for one delivery. Implement it
// and hand it to NewSettler, which adds the settle-once and staleness rules so
// they are not written once per protocol, subtly differently.
//
// Every method concerns the single delivery the SettleOps was built for; the
// settle token is the implementation's own business and deliberately never
// appears in this interface. A token that can be extracted can be stored, and
// what would be stored is a lease with a clock and a quota on it, not a value.
type SettleOps interface {
	// Ack settles the delivery as handled with the broker.
	Ack(ctx context.Context) error

	// Nack settles the delivery as not handled. Some protocols have nothing to
	// send — leaving a message unacknowledged until its lease lapses *is* the
	// nack — in which case this records the reason and returns nil.
	Nack(ctx context.Context, reason string) error

	// Keepalive extends the delivery's lease, reporting whether anything was
	// extended. A protocol with no per-delivery lease returns (false, nil).
	Keepalive(ctx context.Context) (bool, error)

	// Valid reports whether the settle token is still good, and why not when
	// it is not. A token can outlive its validity — an SQS receipt handle
	// expires with the visibility window, an AMQP delivery tag is re-pointed by
	// a channel reconnect — and on some protocols the consequence of using a
	// stale one is not a failure but settling a *different* message. A
	// receiver whose tokens cannot go stale returns (true, "").
	Valid() (bool, string)
}

// StaleError reports a settle attempted against a token that is no longer
// valid. Nothing was sent to the broker. Callers that want to know only
// whether they settled the delivery can ignore it — the accompanying bool is
// false either way — but it is worth logging, because a delivery that outlived
// its lease usually means handling took longer than the configuration allows
// for, which is a fact about the configuration.
type StaleError struct {
	// Reason is the receiver's explanation, in its own vocabulary:
	// "visibility timeout expired", "channel reconnected", "entry reclaimed".
	Reason string
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("delivery can no longer be settled: %s", e.Reason)
}

// IsStale reports whether err, or anything it wraps, is a StaleError.
func IsStale(err error) bool {
	var stale *StaleError
	return errors.As(err, &stale)
}

// NewSettler returns a Settler enforcing the settle-once and staleness rules
// over ops.
func NewSettler(ops SettleOps) Settler {
	return &settler{ops: ops}
}

type settler struct {
	ops     SettleOps
	settled atomic.Bool
}

func (s *settler) Ack(ctx context.Context) (bool, error) {
	return s.settle(func() error { return s.ops.Ack(ctx) })
}

func (s *settler) Nack(ctx context.Context, reason string) (bool, error) {
	return s.settle(func() error { return s.ops.Nack(ctx, reason) })
}

// settle runs do exactly once across every caller that shares this delivery.
//
// Settledness is checked before staleness so that a delivery someone else
// already settled reports the plain "not me" rather than a stale-token error:
// a token that expired after the delivery was settled is nobody's problem, and
// saying so would send a reader looking for a timeout that did not happen.
//
// The claim is then taken before do runs, so two goroutines cannot both reach
// the broker, and released again if do fails. Releasing is the useful
// direction: a failed XACK has settled nothing, so the delivery is still
// outstanding, its settle deadline should still fire, and another subscriber
// attempting the same thing should be allowed to succeed. It costs a window in
// which two callers can each make one failing broker call, which is the same
// number of calls either of them would have made alone.
func (s *settler) settle(do func() error) (bool, error) {
	if s.settled.Load() {
		return false, nil
	}
	if ok, reason := s.ops.Valid(); !ok {
		return false, &StaleError{Reason: reason}
	}
	if !s.settled.CompareAndSwap(false, true) {
		return false, nil
	}
	if err := do(); err != nil {
		s.settled.Store(false)
		return false, err
	}
	return true, nil
}

func (s *settler) Keepalive(ctx context.Context) (bool, error) {
	// Keeping a settled delivery alive is not an error, but there is no lease
	// left to extend, so it reports that nothing was extended.
	if s.settled.Load() {
		return false, nil
	}
	if ok, reason := s.ops.Valid(); !ok {
		return false, &StaleError{Reason: reason}
	}
	return s.ops.Keepalive(ctx)
}

type settlerContextKey struct{}

// WithSettler returns ctx carrying s, for a receiver to call on the context it
// delivers a message with. Everything downstream of the delivery — across
// transforms, async queues, and bus hops — can then settle it.
func WithSettler(ctx context.Context, s Settler) context.Context {
	return context.WithValue(ctx, settlerContextKey{}, s)
}

// SettlerFromContext returns the Settler stored by WithSettler, or nil when the
// message did not arrive over a transport that acknowledges. A nil return is
// the ordinary case for most messages and is not an error: code that settles
// should treat it as "nothing to settle" so that shared handling can run
// against any receiver.
func SettlerFromContext(ctx context.Context) Settler {
	s, _ := ctx.Value(settlerContextKey{}).(Settler)
	return s
}
