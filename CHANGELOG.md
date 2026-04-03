# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
