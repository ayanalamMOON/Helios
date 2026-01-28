package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("helios")

// StartSpan starts a new trace span
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

// RecordError records an error in the current span
func RecordError(span trace.Span, err error) {
	if span != nil && err != nil {
		span.RecordError(err)
	}
}

// SetSpanStatus sets the status of a span
func SetSpanStatus(span trace.Span, code int) {
	if span != nil {
		span.SetAttributes(attribute.Int("http.status_code", code))
	}
}

// TraceID returns the trace ID from context
func TraceID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().HasTraceID() {
		return span.SpanContext().TraceID().String()
	}
	return ""
}

// SpanID returns the span ID from context
func SpanID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().HasSpanID() {
		return span.SpanContext().SpanID().String()
	}
	return ""
}

// Helper functions for common operations

// TraceOperation wraps an operation with tracing
func TraceOperation(ctx context.Context, operationName string, fn func(context.Context) error) error {
	ctx, span := StartSpan(ctx, operationName)
	defer span.End()

	err := fn(ctx)
	if err != nil {
		RecordError(span, err)
	}

	return err
}

// TraceOperationWithResult wraps an operation with tracing and returns a result
func TraceOperationWithResult[T any](ctx context.Context, operationName string, fn func(context.Context) (T, error)) (T, error) {
	ctx, span := StartSpan(ctx, operationName)
	defer span.End()

	result, err := fn(ctx)
	if err != nil {
		RecordError(span, err)
	}

	return result, err
}

// Timer measures operation duration
type Timer struct {
	start time.Time
	name  string
}

// NewTimer creates a new timer
func NewTimer(name string) *Timer {
	return &Timer{
		start: time.Now(),
		name:  name,
	}
}

// Stop stops the timer and records the duration
func (t *Timer) Stop() time.Duration {
	duration := time.Since(t.start)
	fmt.Printf("[TIMER] %s: %v\n", t.name, duration)
	return duration
}
