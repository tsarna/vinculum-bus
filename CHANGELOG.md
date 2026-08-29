# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
