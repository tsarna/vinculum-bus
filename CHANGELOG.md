# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
