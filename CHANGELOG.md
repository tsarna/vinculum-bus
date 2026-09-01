# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Settling follows the work, not the hand-off.** A delivery used to be
  settled where it was made, on the assumption that a subscriber returning
  meant the work was done. That is exact only while delivery is synchronous:
  a bus returns once the message is on its channel, and an
  `AsyncQueueingSubscriber` once it is on the queue — so the broker was told
  the message had been handled before anything had handled it, and a failure
  afterwards had nothing left to redeliver.

  `SettleOnReturn(ctx, callee, err)` is the rule, and it is one function
  because it is the same five lines at every settle point:

  - `err != nil` nacks, whatever the mode. Under manual this looks like taking
    back a decision the configuration asked for, and is not — an unsettled
    delivery there is bounded by a settle deadline whose expiry nacks anyway,
    so the two differ in latency and in whether the broker is told why.
  - a synchronous callee returning nil acks, if the delivery is
    framework-settled.
  - a callee that says it *deferred* settles nothing here; it settles at its
    own completion point.

  `SettleRefused(ctx, reason)` is the other half: a message nothing ran — a
  full queue, a closed subscriber, a bus that dropped it — nacks in either
  mode. Nothing ran, so there is no decision to preempt.

- **`Disposition`, `Dispositioned` and `DispositionOf`.** What a subscriber's
  return says about the delivery, in three answers rather than the two a
  boolean could hold:

  - `Handled` — the work is done and the returned error is its outcome. The
    zero value, so it is what a subscriber says by saying nothing, which is
    right for every leaf and every pass-through wrapper.
  - `Deferred` — accepted for later; the subscriber settles at its own
    completion point. An *error* from a deferring subscriber is a refusal
    rather than a failure, and nacks.
  - `Observed` — looked at, no responsibility taken. A debugging tap, a
    printer, an audit logger. Never a settle point and not expected to become
    one.

  `Observed` exists because reading a tap's nil return as "handled" would let a
  debugging tool acknowledge a broker message it merely printed, and reading
  its *error* as a refusal would send real traffic back for redelivery because
  a printer could not format something. Observation must not change delivery.
  Borrowing `Deferred` for this would get one of the three right and leave an
  observer indistinguishable from a deferrer that has gone missing.

  `DispositionOf` walks `Unwrap() Subscriber`, so a transform or a logger
  cannot hide what it wraps — a wrapper that did would make its caller settle
  on the wrapper's own return, with no error and no log line anywhere.

  Which way it is safe to be wrong is part of the contract: claiming `Handled`
  when you did not handle costs a premature acknowledgement, which is
  unrecoverable, so anything unsure of itself should not claim it.

- **A message that only observers matched is undelivered.** Nobody took it up,
  so it is counted and settled exactly as one that matched no subscriber at
  all. This is what keeps attaching a tap from silencing `UndeliveredTotal()`,
  which is the diagnostic for a topic pattern that was meant to match.

- **A standalone `LoggingSubscriber` is an observer.** With nothing wrapped it
  is a tap, and it no longer acknowledges what it prints or nacks what it fails
  to print. Wrapping something is unchanged — the question passes through
  `Unwrap` to whatever is actually doing the work.

- **`Settler.Auto()` and the `AutoSettle()` option**, distinguishing a delivery
  the framework settles from one the configuration will. It rides on the handle
  rather than beside it on the context: two keys that have to agree is a bug
  factory, and hanging it here means there is nowhere to put one without the
  other.

- **`WithoutSettler(ctx)`**, for deriving a new message from a delivery rather
  than handing the same one on. Responsibility should not propagate past where
  it was discharged — three derived messages racing to settle one delivery
  would make the winner arbitrary.

- **`Unwrap() Subscriber` on `AsyncQueueingSubscriber`, `TransformingSubscriber`
  and `LoggingSubscriber`.** Needed by `Defers`, and independently useful:
  there was no way to ask what a wrapped subscriber actually is.

### Changed

- **Delivery into a bus no longer counts as handling.** `basicEventBus`
  reports `DefersDelivery`, and `deliverAsync` settles once per subscriber
  after that subscriber has answered — so an acknowledgement follows the work
  across any number of bus hops. Under fan-out it still settles once: an
  acknowledgement means someone took responsibility, not that everyone
  finished.

- **A message the bus refuses is nacked rather than silently dropped.**
  `Publish` returns nil whether or not the message was accepted, and
  `basicEventBus.OnEvent` discards even that, so nothing upstream could learn
  that a message was dropped. A queue-full, not-started or stopped bus now
  says so to the delivery's settler.

- **A message that matched no subscriber settles according to whether anyone
  asked to hear about it.** With `WithUndeliverable(false)` — the default — it
  is acknowledged: nothing asked, and a topic matching no subscription is a
  routing outcome the configuration chose. Nacking there would turn an
  unsubscribed topic into a redelivery loop. With the option on, the
  republished message reaches a real subscription and *that* decides; only a
  `$undeliverable` which itself matches nothing is nacked, which is the case
  where the author asked to be told and then did not listen.

- **A panic in the async drain goroutine nacks before it unwinds.** The panic
  still brings the process down; the difference is that the broker hears why
  now instead of waiting out a lease.

- **A nack reason is bounded once, in `Settler.Nack`.** Settle points hand it
  whatever error the work returned, which can be a rendered multi-line
  diagnostic, and it goes on to become a dead-letter header. Truncation is at
  the one point every nack passes through, on a rune boundary, rather than
  once per protocol and differently each time.

## [0.18.0] - 2026-08-30

### Added

- **A vocabulary for settling an inbound delivery.** Acknowledgement is a
  property of the delivery — of the message that arrived from a broker — and not
  of the payload, nor of whichever subscriber handles it. It cannot travel in
  `fields`, which are rewritten per subscription with that subscription's own
  topic captures, so there has been no way for anything past the receiver to
  acknowledge what it handled. It travels on the context instead, which crosses
  every hop and which the async queue already preserves
  (`context.WithoutCancel` drops cancellation and keeps values):

  ```go
  // in the receiver
  ctx = bus.WithSettler(ctx, bus.NewSettler(myDeliveryOps))

  // anywhere downstream, whatever the protocol
  if s := bus.SettlerFromContext(ctx); s != nil {
      settled, err := s.Ack(ctx)
  }
  ```

  A receiver implements `SettleOps` — `Ack`, `Nack`, `Keepalive`, and `Valid` —
  for one delivery, and `NewSettler` supplies the two rules that are easy to get
  subtly wrong once per protocol. A delivery settles **exactly once**: the first
  `Ack` or `Nack` wins and every later call is a no-op reporting `false`, so an
  acknowledgement means "someone took responsibility" rather than "everyone
  finished". And a settle against a token that has gone stale — `Valid` reports
  it, an SQS receipt handle expiring with its visibility window, an AMQP
  delivery tag re-pointed by a reconnect — never reaches the broker at all,
  returning a `*StaleError` saying why. That case is not merely a failed
  acknowledgement on every protocol: a stale AMQP tag acknowledges a *different*
  message.

  The bus itself never settles anything and never inspects a `Settler`.
  Settling is the receiver's contract with its broker, and the bus knows nothing
  about brokers; what is added here is only the vocabulary its consumers need in
  common, in the one module all of them already depend on.

## [0.17.0] - 2026-08-29

### Added

- **The bus can say how full it is, and what it has thrown away.** A process
  whose queue is saturated is still connected to everything, still passing every
  check, and dropping messages on the floor — and from outside it is
  indistinguishable from a healthy one. `EventBus` gains four accessors so it is
  not:

  ```go
  bus.QueueDepth()       // messages accepted but not yet dispatched
  bus.QueueCapacity()    // the buffer it was built with
  bus.DroppedTotal()     // messages it could not accept
  bus.UndeliveredTotal() // messages no subscriber matched
  ```

  Depth and capacity are `len`/`cap` — lock-free, safe from any goroutine, cheap
  to poll forever. The two counters are plain atomics rather than metric
  readbacks, so they can be read with no metrics backend configured, which is
  what lets a program threshold on them.

  Depth *and* capacity, not a ratio: the ratio is trivially derived, and "4096 of
  4096" is what belongs in a log line.

- **A dropped message is counted, not only logged.** `accept` ended in a bare
  `default:` that logged a warning and moved on. A log line cannot be
  thresholded, alerted on, or read back by the process that emitted it. The
  warning stays and now carries the bus, topic, and operation; beside it the
  `dropped` atomic and, when a `MeterProvider` is configured, a
  `messaging.client.dropped.messages` counter attributed by topic and operation.
  Kept out of `messaging.client.errors`, so a drop and a subscriber failure stay
  separable.

- **A message that matched no subscriber is counted.** The delivery loop was
  already looking — it walks every subscriber's matchers and finds none that
  answer — and said nothing about it: no log line, no metric, no error. A
  receiver wired to a topic nothing consumes behaved exactly like a healthy idle
  system. Counting it costs one boolean per publish, on both the async and the
  sync path, and shows up as `UndeliveredTotal()` and
  `messaging.client.undelivered.messages`.

- **`WithUndeliverable(true)`: republish what nothing matched.** Opt-in per bus.
  An unmatched message is republished under the reserved topic `$undeliverable`
  carrying its **original context** and payload, with the topic that failed to
  route reachable through `UndeliverableTopicFromContext(ctx)`:

  ```go
  eventBus, _ := bus.NewEventBus().WithUndeliverable(true).Build()
  ```

  The context riding along verbatim is the point: whatever it carries — a
  settler for the inbound delivery, a trace, a deadline — reaches the handler
  that gets to act on it, so an unroutable message can be rejected with a real
  reason instead of waiting out a timeout and being redelivered into the same
  hole.

  A `$`-prefixed topic is **never** republished. Without that rule a bus with the
  attribute on and no `$undeliverable` subscriber — the normal state for anyone
  who enables it and forgets — would feed itself forever. Such a message is still
  counted, which is exactly the number that says the handler is missing.

  Off by default: publishing to a topic nobody wants is normal in pub/sub and
  must stay free, and every unmatched publish would otherwise become a second
  one on the same delivery goroutine. A synchronous publish republishes too, and
  its return value is unchanged — a caller who published to nobody never asked
  to be told.

- **`AsyncQueueingSubscriber.DroppedTotal()`** — a subscriber with a queue is a
  second, independent backpressure point, so counting only the bus's drops would
  miss the one-slow-handler case. The overflow policy is deliberately unchanged:
  this queue's users are outbound senders and subscription actions, for whom
  drop-newest under sustained overload is defensible. What was missing was the
  number saying it had happened.

### Changed

- **`AsyncQueueingSubscriber.QueueSize()` is now `QueueDepth()`**, matching the
  bus accessor of the same meaning. `QueueCapacity()` is unchanged.

### Fixed

- **A data race in `mockClient`** (tests only). `AutoReconnector` reconnects from
  its own goroutine, so the flags it set were read by the test while that
  goroutine was still writing them, and `go test -race` failed on it.

## [0.16.0] - 2026-08-14

### Added

- **`ReportedError`, for a subscriber that has already reported a failure
  itself.** A subscriber sometimes knows more about a delivery failure than the
  bus can: where in a configuration file the handler was written, say, and what
  the failing line said. Such a subscriber logs the better report itself, and
  the bus then logs the same failure again in the plain form the better one
  replaced. Returning an error that implements `ReportedError` says the failure
  has been reported and the bus's own log line would only repeat it:

  ```go
  type reported struct{ error }

  func (reported) AlreadyReported() {}
  func (e reported) Unwrap() error  { return e.error }

  return reported{err}   // logged by the subscriber, not by the bus
  ```

  Only the log line is skipped. The error is still returned to the caller, still
  recorded on the delivery span, and still counted, so a dead-letter path or an
  ack decision reading the outcome sees exactly what it did before — and
  delegating `Error` and `Unwrap`, as above, leaves the text and the wrapped type
  intact for anything that inspects them.

  The mark is a property of one error rather than of a subscriber, deliberately:
  a subscriber typically reports the failures it can render and leaves the rest
  to the bus, and marking per error is what keeps those two cases from silencing
  each other. Nothing changes for a subscriber that does not return a marked
  error.

## [0.15.1] - 2026-06-26

### Changed

- Dependency updates: `go.opentelemetry.io/otel` and related modules to v1.44.0,
  `go.uber.org/zap` to v1.28.0.

### Fixed

- Re-release to repair the module checksum. The `v0.15.0` tag was inadvertently
  moved after publication, so its content no longer matched the hash recorded in
  the Go checksum database, breaking `go build`/`go mod` for downstream consumers.
  `v0.15.0` should be considered poisoned; use `v0.15.1` or later instead. No API
  changes from `v0.15.0`.

## [0.15.0] - 2026-05-25

### Changed

Now licensed under the Apache 2.0 license.

## [0.14.0] - 2026-04-24

### Added

- **`EventBusMessage.Fields`** — new `Fields map[string]string` on `EventBusMessage` carries subscriber-local delivery metadata (e.g. topic pattern extractions, enrichment added by transforms on the final hop). The bus publish/subscribe paths leave `Fields` unset, preserving the existing semantic that fields do not propagate through busses. Transforms can now read and write `msg.Fields`, and a transform that mutates it will have its changes delivered to the wrapped subscriber's `OnEvent` (see the `TransformingSubscriber` change below).

### Changed

- **`transform.ApplyTransforms` signature (breaking)** — now takes an additional `fields map[string]string` parameter between `payload` and `transforms`, used to seed the initial message's `Fields`. Callers that were not populating fields should pass `nil`.
- **`subutils.TransformingSubscriber.OnEvent` — transforms can now mutate `Fields`** — the wrapped subscriber is now delivered `transformed.Fields` rather than the original caller-supplied fields. A transform that adds, modifies, or removes entries in `msg.Fields` will have those changes visible on delivery. Transforms that do not touch `Fields` behave unchanged.
- **Transform message-copy sites preserve `Fields`** — `AddTopicPrefix`, `ReplaceInTopic`, `TransformOnPattern`, and `ModifyPayload` now carry `Fields` through when they allocate a new `EventBusMessage`.

### Removed

- **`subutils.asyncMessage` wrapper** — `AsyncQueueingSubscriber` previously wrapped `EventBusMessage` in an unexported struct to carry fields alongside each queued message. Now that `EventBusMessage` carries `Fields` natively, the wrapper is gone and the internal queue is `chan bus.EventBusMessage`. No effect on the public API.

## [0.13.0] - 2026-04-23

### Added

- **Tracing for `AsyncQueueingSubscriber`** — `subutils.AsyncQueueingSubscriber` now accepts a `trace.TracerProvider` via the new `WithTracerProvider` fluent option (and an optional `WithName` for instrumentation-scope and attribute naming). When configured, each message processed in the background goroutine is wrapped in a new-root `SpanKindConsumer` span (`process <topic>`, `on_subscribe <topic>`, `on_unsubscribe <topic>`, `tick`, `passthrough <topic>`) linked to the caller's span context. This preserves the causal link to the upstream publish span without tying the async span to the producer's already-ended lifecycle — the same pattern the event bus uses in `deliverAsync`. Ticker ticks now also flow through the shared dispatch path so they are traced uniformly.

## [0.12.0] - 2026-04-23

- **`topicmatch` package** — thin wrapper around `mqttpattern` that enforces the MQTT 5.0 §4.7.2 rule for `$`-prefixed topics. Exports `Matches`, `Extract`, `Exec`, and `HasExtractions`; the latter is a passthrough. All internal topic matching (subscriber delivery, `transform` pipeline, extraction detection) now routes through this package, making it the sole importer of `mqttpattern`.

### Changed

- **Wildcard subscriptions no longer match `$`-prefixed topics** — per MQTT 5.0 §4.7.2, a topic filter starting with `+` or `#` does not match topics beginning with `$`. Subscribers to `#` or `+/...` will no longer receive events published to reserved topics such as `$metrics`. Exact subscriptions (e.g. `"$metrics"`) and patterns whose first segment is `$`-prefixed (e.g. `"$sys/#"`) continue to match as before. Transform functions (`DropTopicPattern`, `IfPattern`, `IfElsePattern`, `TransformOnPattern`) follow the same rule.

## [0.11.1] - 2026-04-22

### Fixed

- **Async delivery context cancellation** — `deliverAsync` and `AsyncQueueingSubscriber.processMessage` now use `context.WithoutCancel` to detach from the producer's context. Previously, a canceled producer context (e.g. a completed HTTP request) would cause downstream `OnEvent` calls to fail with "context canceled". Context values including OTel baggage are preserved. The tracer path in `deliverAsync` also switched from `context.Background()` to `context.WithoutCancel` to preserve baggage propagation across async boundaries.

## [0.11.0] - 2026-04-08

### Changed

- **OTel metrics replaces o11y.MetricsProvider abstraction** — the `MetricsProvider`, `Counter`, `Histogram`, `Gauge`, `Label`, and `ObservabilityConfig` types have been removed from the `o11y` package. The event bus now accepts a `metric.MeterProvider` directly via `WithMeterProvider(metric.MeterProvider)` on the builder (replacing the removed `WithMetrics` method). This is a breaking API change.

- **Metric names follow OTel semantic conventions** — standard `messaging.client.*` names are used where applicable (`messaging.client.sent.messages`, `messaging.client.operation.duration`, `messaging.client.errors`). Eventbus-specific metrics use an `eventbus.*` namespace (`eventbus.subscriptions`, `eventbus.unsubscriptions`, `eventbus.active_subscribers`). All metrics carry `messaging.system=eventbus`, `messaging.destination.name`, and `vinculum.bus.name` (when set) attributes.

- **Standalone metrics provider rewritten as OTel SDK exporter** — `StandaloneMetricsProvider` is replaced by `StandaloneExporter` (implementing `sdkmetric.Exporter`) and `NewStandaloneMeterProvider()` which returns a standard `*sdkmetric.MeterProvider`. The OTel SDK handles aggregation and the periodic publish loop; the exporter converts metric data into `MetricsSnapshot` and publishes to the bus. Shutdown is now via `mp.Shutdown(ctx)` instead of `Stop()`.

- **MetricsSnapshot format updated** — `Counters` changed from `map[string]int64` to `map[string]float64`. `Histograms` changed from `map[string][]float64` (raw values) to `map[string]HistogramSnapshot` (pre-aggregated buckets with `Count`, `Sum`, `Bounds`, `BucketCounts`).

### Removed

- **`otel` sub-package deleted** — the `otel.Provider` adapter (which bridged `o11y.MetricsProvider` to OTel) is no longer needed since consumers now use `metric.MeterProvider` directly.

## [0.10.0] - 2026-04-03

### Changed

- **OTel tracing replaces o11y.TracingProvider abstraction** — the `TracingProvider`, `Span`, and `SpanStatusCode` types have been removed from the `o11y` package. The event bus now accepts a `trace.TracerProvider` directly via `WithTracerProvider(trace.TracerProvider)` on the builder (replacing the removed `WithTracing` and `WithObservability` methods). This is a breaking API change.

### Added

- **Producer spans for `Publish` and `PublishSync`** — when a `TracerProvider` is configured, both publish methods create a `SpanKindProducer` span (`publish <topic>`) with OTel messaging semantic convention attributes: `messaging.system=vinculum`, `messaging.destination.name`, `messaging.operation.type=publish`, `messaging.operation.name=publish`, and `vinculum.bus.name` (when the bus has a name). The span context is stored in the message so delivery spans can link to it.

- **Per-subscriber consumer spans for `PublishSync`** — each subscriber delivery in a synchronous publish is wrapped in a `SpanKindConsumer` child span (`process <topic>`), giving a complete trace tree: `publish → process → subscriber work`.

- **Per-subscriber linked consumer spans for async `Publish`** — each subscriber delivery from an async publish creates a new root `SpanKindConsumer` span (`process <topic>`) linked to the producer span, following the [OTel messaging semantic conventions](https://opentelemetry.io/docs/specs/otel/trace/semantic_conventions/messaging/) recommendation for async pub/sub boundaries.

- **`vinculum.bus.name` attribute** — all spans carry a `vinculum.bus.name` custom attribute (when the bus was built with `WithName`), and the instrumentation scope is `vinculum-bus/<name>`, making spans filterable per bus instance in tracing backends.

## [0.9.3] - 2025-11-15

Previous releases.
