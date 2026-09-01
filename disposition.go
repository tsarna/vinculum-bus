package bus

// A subscriber's OnEvent returning nil normally means the message was handled,
// and that is what lets a delivery be settled where it was made: the caller has
// the outcome in hand. Not every subscriber is making that claim.
//
// Some return before the work happens — a bus once the message is on its
// channel, an async queue once it is on the queue, a state machine once the
// event is enqueued — and for those a nil return means "accepted for later".
//
// Others never make the claim at all. A debugging tap, a printer, an audit
// logger attached to a live bus: they look at the message and take no
// responsibility for it. Reading their return as "handled" would let a
// debugging tool acknowledge a broker message it merely printed, which is the
// one thing an observer must never do.
//
// Disposition is how a subscriber says which of the three it is, and
// DispositionOf is how a settle point asks.

// Disposition is what a subscriber's return says about the delivery it was
// given.
type Disposition int

const (
	// Handled means the work is done and the returned error is its outcome.
	//
	// This is the zero value, so it is what every subscriber says by saying
	// nothing — which is correct for every leaf, and for every wrapper that
	// passes a message along. The unusual cases are the ones that declare
	// themselves.
	Handled Disposition = iota

	// Deferred means the message was accepted for later processing. A nil
	// return is not a claim that anything happened; the subscriber settles at
	// its own completion point.
	//
	// An error from a deferring subscriber is a *refusal* rather than a
	// failure — it did not defer, it declined — and a settle point nacks it,
	// because nothing ran and nothing will.
	Deferred

	// Observed means the subscriber looked at the message and took no
	// responsibility for it. It is never a settle point and is not expected to
	// become one, which is the difference between an observer and a deferrer
	// that has gone missing.
	//
	// Neither its success nor its failure reaches the broker: an observer that
	// cannot format a message for printing has a problem of its own, and
	// nacking real traffic over it would be observation changing delivery.
	//
	// A message that only observers matched has been taken up by nobody, so
	// the bus counts it undelivered and it takes the same path as a message
	// that matched no subscriber at all.
	Observed
)

func (d Disposition) String() string {
	switch d {
	case Deferred:
		return "deferred"
	case Observed:
		return "observed"
	default:
		return "handled"
	}
}

// Dispositioned is implemented by a subscriber whose return means something
// other than "handled". Not implementing it is the same as reporting Handled.
//
// # Which way it is safe to be wrong
//
// Claiming Handled when you did not handle costs a premature acknowledgement:
// the broker is told the work happened before it did, and if the work then
// fails there is nothing left to redeliver. That one is unrecoverable.
//
// Claiming Deferred when you will never settle costs an unsettled message,
// which a broker lease or a settle_timeout eventually recovers — but say
// Observed instead if you will never settle, because a deferral nobody honours
// is indistinguishable from one that went missing, and an error from a
// deferrer nacks where an observer's is ignored.
//
// So anything unsure of itself should not claim Handled.
//
// This is a method rather than a type switch because a disposition is not
// always a property of the type. A producer that returns before the broker has
// taken the message defers; the same producer waiting for the acknowledgement
// does not, and which one it is can be a configuration choice.
type Dispositioned interface {
	DeliveryDisposition() Disposition
}

// DispositionOf reports what s's return will mean, seeing through wrappers.
//
// Wrappers are why this is not a plain type assertion. A transform, a logger or
// a filter passes the message to something that may defer or merely observe,
// and a wrapper that hides that makes its caller settle on the wrapper's own
// return — reintroducing the premature acknowledgement, invisibly, with no
// error and no log line. A wrapper participates by implementing
//
//	Unwrap() Subscriber
//
// which costs one line and is independently useful: there is otherwise no way
// to ask what a wrapped subscriber actually is.
//
// The first definite answer wins. A queue that wraps an observer really does
// defer, whatever is behind it, and its own drain asks the question again of
// what it wraps.
//
// A nil subscriber is Handled, which is what makes SettleOnReturn's nil callee
// mean "no callee to ask" rather than "ask and get an answer".
func DispositionOf(s Subscriber) Disposition {
	for s != nil {
		if d, ok := s.(Dispositioned); ok {
			if disposition := d.DeliveryDisposition(); disposition != Handled {
				return disposition
			}
		}

		u, ok := s.(interface{ Unwrap() Subscriber })
		if !ok {
			return Handled
		}

		s = u.Unwrap()
	}

	return Handled
}
