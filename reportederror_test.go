package bus

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
)

// reportedTestError marks an error as already reported, the way a subscriber
// that renders its own failures is expected to: by wrapping, so the text and
// the wrapped type both survive.
type reportedTestError struct{ error }

func (reportedTestError) AlreadyReported() {}

func (e reportedTestError) Unwrap() error { return e.error }

// failingSubscriber returns err from every OnEvent.
type failingSubscriber struct {
	BaseSubscriber
	err error
}

func (s *failingSubscriber) OnEvent(context.Context, string, any, map[string]string) error {
	return s.err
}

// A subscriber that has already reported a failure in a form the bus cannot
// produce should not have the bus say it again, less well. Everything else
// about the delivery is unchanged, which is why only the log line is skipped.
func TestReportedErrorSkipsTheBusLogLine(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantLogs int
	}{
		{name: "plain error is logged", err: errors.New("boom"), wantLogs: 1},
		{name: "reported error is not", err: reportedTestError{errors.New("boom")}, wantLogs: 0},
		{
			// Marked deep, since a subscriber may wrap on the way out.
			name:     "reported error wrapped again is not",
			err:      fmt.Errorf("delivering: %w", reportedTestError{errors.New("boom")}),
			wantLogs: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zap.ErrorLevel)
			eventBus, err := NewEventBus().WithLogger(zap.New(core)).Build()
			if err != nil {
				t.Fatalf("Build() returned error: %v", err)
			}
			if err := eventBus.Start(); err != nil {
				t.Fatalf("Start() returned error: %v", err)
			}
			defer eventBus.Stop() //nolint:errcheck

			ctx := context.Background()
			sub := &failingSubscriber{err: tc.err}
			if err := eventBus.Subscribe(ctx, "test", sub); err != nil {
				t.Fatalf("Subscribe() returned error: %v", err)
			}
			if err := eventBus.Publish(ctx, "test", "payload"); err != nil {
				t.Fatalf("Publish() returned error: %v", err)
			}
			time.Sleep(50 * time.Millisecond)

			if got := logs.FilterMessage("Error in OnEvent").Len(); got != tc.wantLogs {
				t.Errorf("bus logged %d times, want %d", got, tc.wantLogs)
			}
		})
	}
}

// The mark says who reported the failure, not whether it happened: a caller
// that reads the outcome — a dead-letter path, an ack decision — must see the
// same error it always did.
func TestReportedErrorStillReachesTheCaller(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	if err := eventBus.Start(); err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}
	defer eventBus.Stop() //nolint:errcheck

	underlying := errors.New("boom")
	sub := &failingSubscriber{err: reportedTestError{underlying}}

	ctx := context.Background()
	if err := eventBus.Subscribe(ctx, "test", sub); err != nil {
		t.Fatalf("Subscribe() returned error: %v", err)
	}

	err = eventBus.PublishSync(ctx, "test", "payload")
	if err == nil {
		t.Fatal("PublishSync should return the subscriber's error")
	}
	if !errors.Is(err, underlying) {
		t.Errorf("the wrapped error should still be reachable, got %v", err)
	}
	if err.Error() != "boom" {
		t.Errorf("the text should be the subscriber's own, got %q", err.Error())
	}
}
