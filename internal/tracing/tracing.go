package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

type Tracer struct {
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
	enabled  bool
}

type TracerConfig struct {
	Enabled        bool
	Endpoint       string
	ServiceName    string
	ServiceVersion string
	Environment    string
}

func NewTracer(cfg TracerConfig) (*Tracer, error) {
	t := &Tracer{
		enabled: cfg.Enabled,
	}

	if !cfg.Enabled {
		t.tracer = noopTracer()
		return t, nil
	}

	ctx := context.Background()

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			semconv.DeploymentEnvironment(cfg.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	t.provider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(t.provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	t.tracer = t.provider.Tracer(cfg.ServiceName)

	return t, nil
}

func (t *Tracer) StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return t.tracer.Start(ctx, name, opts...)
}

func (t *Tracer) AddSpanAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span != nil && span.IsRecording() {
		span.SetAttributes(attrs...)
	}
}

func (t *Tracer) RecordError(ctx context.Context, err error, opts ...trace.EventOption) {
	span := trace.SpanFromContext(ctx)
	if span != nil && span.IsRecording() {
		span.RecordError(err, opts...)
	}
}

func (t *Tracer) GetTraceID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span == nil {
		return ""
	}
	return span.SpanContext().TraceID().String()
}

func (t *Tracer) GetSpanID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span == nil {
		return ""
	}
	return span.SpanContext().SpanID().String()
}

func (t *Tracer) InjectTraceContext(ctx context.Context) map[string]string {
	carrier := make(map[string]string)
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(carrier))
	return carrier
}

func (t *Tracer) ExtractTraceContext(ctx context.Context, carrier map[string]string) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(carrier))
}

func (t *Tracer) Shutdown(ctx context.Context) error {
	if t.provider != nil {
		return t.provider.Shutdown(ctx)
	}
	return nil
}

func (t *Tracer) IsEnabled() bool {
	return t.enabled
}

func noopTracer() trace.Tracer {
	return otel.Tracer("noop")
}

type SpanRecorder struct {
	spans []RecordedSpan
}

type RecordedSpan struct {
	Name       string
	TraceID    string
	SpanID     string
	ParentID   string
	Attributes map[string]interface{}
}

func (r *SpanRecorder) ExportSpans(spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		sc := span.SpanContext()
		rs := RecordedSpan{
			Name:    span.Name(),
			TraceID: sc.TraceID().String(),
			SpanID:  sc.SpanID().String(),
		}

		if sc.IsRemote() {
			rs.ParentID = ""
		}

		rs.Attributes = make(map[string]interface{})
		for _, attr := range span.Attributes() {
			rs.Attributes[string(attr.Key)] = attr.Value.AsInterface()
		}

		r.spans = append(r.spans, rs)
	}
	return nil
}

func (r *SpanRecorder) ResourceSpans() []*resource.Resource {
	return nil
}

func (r *SpanRecorder) GetSpans() []RecordedSpan {
	return r.spans
}

func (r *SpanRecorder) Reset() {
	r.spans = r.spans[:0]
}
