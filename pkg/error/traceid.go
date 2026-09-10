package errx

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

// traceIDFromContext returns the hex-encoded trace and span IDs of the OTel
// span carried by ctx. Both are empty when ctx carries no span or the span
// context is not sampled into a valid pair of IDs.
func traceIDFromContext(ctx context.Context) (traceID, spanID string) {
	if ctx == nil {
		return "", ""
	}

	sc := trace.SpanContextFromContext(ctx)

	if sc.HasTraceID() {
		traceID = sc.TraceID().String()
	}

	if sc.HasSpanID() {
		spanID = sc.SpanID().String()
	}

	return traceID, spanID
}
