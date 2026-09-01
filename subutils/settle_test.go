package subutils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsarna/vinculum-bus"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// recordingOps counts what reached "the broker", so a test can say when the
// settle happened rather than only that it did.
type recordingOps struct {
	mu      sync.Mutex
	acks    int
	nacks   int
	reasons []string
}

func (o *recordingOps) Ack(context.Context) error { o.mu.Lock(); o.acks++; o.mu.Unlock(); return nil }

func (o *recordingOps) Nack(_ context.Context, reason string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.nacks++
	o.reasons = append(o.reasons, reason)
	return nil
}

func (o *recordingOps) Keepalive(context.Context) (bool, error) { return false, nil }
func (o *recordingOps) Valid() (bool, string)                   { return true, "" }

func (o *recordingOps) counts() (acks, nacks int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.acks, o.nacks
}

func (o *recordingOps) firstReason() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.reasons) == 0 {
		return ""
	}
	return o.reasons[0]
}

// gatedSubscriber holds the drain goroutine inside OnEvent until released, so a
// test can look at the broker while the work is provably still running.
type gatedSubscriber struct {
	bus.BaseSubscriber
	entered     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
	err         error
}

func newGatedSubscriber() *gatedSubscriber {
	return &gatedSubscriber{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

// Release is idempotent so a test can release the gate to observe what happens
// next and still defer a release that guarantees the drain goroutine is never
// left parked — a test that fails an assertion must fail, not hang on Close.
func (g *gatedSubscriber) Release() { g.releaseOnce.Do(func() { close(g.release) }) }

func (g *gatedSubscriber) OnEvent(context.Context, string, any, map[string]string) error {
	select {
	case g.entered <- struct{}{}:
	default:
	}
	<-g.release
	return g.err
}

// The defect this queue used to introduce, stated as a test. OnEvent returns
// the moment the message is queued, and if that return were taken as the
// outcome the broker would be told the work was done before it started.
func TestQueueSettlesWhenTheWorkFinishesNotAtEnqueue(t *testing.T) {
	gate := newGatedSubscriber()
	async := NewAsyncQueueingSubscriber(gate, 10).Start()

	// Order matters: Close waits for the drain goroutine, so the gate has to be
	// released first. Deferred last means run first.
	defer async.Close()
	defer gate.Release()

	ops := &recordingOps{}
	ctx := bus.WithSettler(context.Background(), bus.NewSettler(ops, bus.AutoSettle()))

	require.NoError(t, async.OnEvent(ctx, "work/one", "payload", nil))

	<-gate.entered
	acks, nacks := ops.counts()
	assert.Equal(t, 0, acks, "the work is still running; nothing has been handled yet")
	assert.Equal(t, 0, nacks)

	gate.Release()

	assert.Eventually(t, func() bool {
		acks, _ := ops.counts()
		return acks == 1
	}, 2*time.Second, 5*time.Millisecond, "the acknowledgement follows the work")
}

// A full queue is a refusal, and the caller learns of it the ordinary way: an
// error from a callee that defers. Nothing in this file has to nack by hand —
// the rule in vinculum-bus does it, which is the point of the rule being one
// function rather than a convention each queue re-implements.
func TestAFullQueueNacksThroughItsCaller(t *testing.T) {
	gate := newGatedSubscriber()
	async := NewAsyncQueueingSubscriber(gate, 1).Start()

	defer async.Close()
	defer gate.Release()

	// One message occupies the drain goroutine, one fills the single slot.
	require.NoError(t, async.OnEvent(context.Background(), "work/first", "in flight", nil))
	<-gate.entered
	require.NoError(t, async.OnEvent(context.Background(), "work/second", "buffered", nil))

	ops := &recordingOps{}
	ctx := bus.WithSettler(context.Background(), bus.NewSettler(ops, bus.AutoSettle()))

	err := async.OnEvent(ctx, "work/refused", "nowhere to go", nil)
	require.ErrorIs(t, err, ErrQueueFull)

	bus.SettleOnReturn(ctx, async, err)

	acks, nacks := ops.counts()
	assert.Equal(t, 0, acks, "a message that was never delivered was not handled")
	assert.Equal(t, 1, nacks, "it goes back to the broker instead of being dropped after an ack")
	assert.Contains(t, ops.firstReason(), "queue is full")
}

// A closed queue is the same shape of refusal as a full one.
func TestAClosedQueueNacksThroughItsCaller(t *testing.T) {
	async := NewAsyncQueueingSubscriber(&bus.BaseSubscriber{}, 10).Start()
	async.Close()

	ops := &recordingOps{}
	ctx := bus.WithSettler(context.Background(), bus.NewSettler(ops, bus.AutoSettle()))

	err := async.OnEvent(ctx, "work/one", "payload", nil)
	require.ErrorIs(t, err, ErrSubscriberClosed)

	bus.SettleOnReturn(ctx, async, err)

	_, nacks := ops.counts()
	assert.Equal(t, 1, nacks)
}

// The silent one. Every wrapper in this module has to report what it wraps, or
// a settle point looking at the outermost one sees a synchronous subscriber and
// acknowledges at the moment of enqueue — with no error and no log line. This
// asserts it over the real chain a configuration builds, rather than over
// hand-written stand-ins.
func TestDispositionSeesThroughTheRealWrapperChain(t *testing.T) {
	logger := zap.NewNop()

	async := NewAsyncQueueingSubscriber(&bus.BaseSubscriber{}, 10)
	transforming := NewTransformingSubscriber(async)
	logging := NewLoggingSubscriber(transforming, logger, zapcore.DebugLevel)

	if got := bus.DispositionOf(logging); got != bus.Deferred {
		t.Fatalf("logging -> transforming -> async: the queue at the end decides; got %v", got)
	}

	plain := NewLoggingSubscriber(
		NewTransformingSubscriber(&bus.BaseSubscriber{}), logger, zapcore.DebugLevel)
	if got := bus.DispositionOf(plain); got != bus.Handled {
		t.Fatalf("the same chain around an ordinary subscriber is ordinary; got %v", got)
	}

	// A logger with nothing behind it is a tap: it prints the message and takes
	// no responsibility for it. Reading its nil return as "handled" would let a
	// logger attached to a bus carrying broker deliveries acknowledge a message
	// it merely printed.
	if got := bus.DispositionOf(NewLoggingSubscriber(nil, logger, zapcore.DebugLevel)); got != bus.Observed {
		t.Fatalf("a standalone logger observes rather than handles; got %v", got)
	}

	// And a tap behind a transform is still a tap.
	tapBehindTransform := NewTransformingSubscriber(NewLoggingSubscriber(nil, logger, zapcore.DebugLevel))
	if got := bus.DispositionOf(tapBehindTransform); got != bus.Observed {
		t.Fatalf("a wrapped tap is still a tap; got %v", got)
	}
}

// A standalone logger on a bus carrying acknowledged deliveries must neither
// acknowledge what it prints nor nack what it fails to print.
func TestAStandaloneLoggerSettlesNothing(t *testing.T) {
	logger := NewLoggingSubscriber(nil, zap.NewNop(), zapcore.DebugLevel)

	ops := &recordingOps{}
	ctx := bus.WithSettler(context.Background(), bus.NewSettler(ops, bus.AutoSettle()))

	require.NoError(t, logger.OnEvent(ctx, "work/one", "payload", nil))
	bus.SettleOnReturn(ctx, logger, nil)

	acks, nacks := ops.counts()
	assert.Equal(t, 0, acks, "printing a message is not handling it")
	assert.Equal(t, 0, nacks)
}

// A panic mid-work must reach the broker before it unwinds, or the message sits
// unsettled until its lease lapses with nothing saying why.
//
// The panic is deliberately left to propagate — this changes what the broker
// hears, not what the process does — so the scenario has to run in a child
// process. The assertions are that the nack was made *and* that the panic still
// killed the child, which is the pair that says "nack, then let it continue".
func TestAPanicInTheDrainNacksBeforeItUnwinds(t *testing.T) {
	if os.Getenv("BUS_DRAIN_PANIC_CHILD") == "1" {
		runDrainPanicChild()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestAPanicInTheDrainNacksBeforeItUnwinds", "-test.v")
	cmd.Env = append(os.Environ(), "BUS_DRAIN_PANIC_CHILD=1")
	out, err := cmd.CombinedOutput()

	assert.Error(t, err, "the panic must still bring the process down")
	assert.Contains(t, string(out), "NACKED: panic while handling work/boom: subscriber exploded",
		"the broker is told what happened, and told before the process dies")
}

// printingOps reports the nack from inside the settle itself. Observing it any
// other way is a race the observer loses: the repanic follows the nack
// immediately, so nothing that polls for it ever gets to run.
type printingOps struct{ recordingOps }

func (o *printingOps) Nack(ctx context.Context, reason string) error {
	fmt.Println("NACKED:", reason)
	return o.recordingOps.Nack(ctx, reason)
}

func runDrainPanicChild() {
	panicking := bus.NewEventReceiver(func(context.Context, string, any, map[string]string) error {
		panic("subscriber exploded")
	})

	async := NewAsyncQueueingSubscriber(panicking, 10).Start()
	ctx := bus.WithSettler(context.Background(), bus.NewSettler(&printingOps{}, bus.AutoSettle()))

	_ = async.OnEvent(ctx, "work/boom", "payload", nil)
	time.Sleep(2 * time.Second)
}

// Settling is confined to event messages: a subscribe, unsubscribe, tick or
// pass-through is not a delivery, and a settler on one of those contexts
// belongs to some other message.
func TestControlMessagesDoNotSettle(t *testing.T) {
	async := NewAsyncQueueingSubscriber(&bus.BaseSubscriber{}, 10).Start()
	defer async.Close()

	ops := &recordingOps{}
	ctx := bus.WithSettler(context.Background(), bus.NewSettler(ops, bus.AutoSettle()))

	require.NoError(t, async.OnSubscribe(ctx, "work/#"))
	require.NoError(t, async.OnUnsubscribe(ctx, "work/#"))
	require.NoError(t, async.PassThrough(bus.EventBusMessage{
		Ctx:     ctx,
		MsgType: bus.MessageTypePassThrough,
		Topic:   "work/reply",
	}))

	time.Sleep(100 * time.Millisecond)

	acks, nacks := ops.counts()
	assert.Equal(t, 0, acks)
	assert.Equal(t, 0, nacks)
}

func TestUnwrapReportsWhatEachWrapperWraps(t *testing.T) {
	leaf := &bus.BaseSubscriber{}

	async := NewAsyncQueueingSubscriber(leaf, 10)
	assert.Equal(t, bus.Subscriber(leaf), async.Unwrap())

	transforming := NewTransformingSubscriber(leaf)
	assert.Equal(t, bus.Subscriber(leaf), transforming.Unwrap())

	logging := NewLoggingSubscriber(leaf, zap.NewNop(), zapcore.DebugLevel)
	assert.Equal(t, bus.Subscriber(leaf), logging.Unwrap())

	assert.Nil(t, NewLoggingSubscriber(nil, zap.NewNop(), zapcore.DebugLevel).Unwrap(),
		"a standalone logger wraps nothing, and says so")
}
