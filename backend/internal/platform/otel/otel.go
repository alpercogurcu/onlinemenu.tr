// Package otel configures the OpenTelemetry SDK with OTLP gRPC exporters
// for traces and metrics. The provider is registered as the global provider
// so instrumentation libraries (otelhttp, etc.) work without explicit wiring.
package otel

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config holds OTLP exporter settings injected via fx.
type Config struct {
	// Endpoint is the OTLP gRPC collector endpoint, e.g. "localhost:4317".
	Endpoint string

	// ServiceName is the logical service name reported to the backend.
	ServiceName string

	// ServiceVersion is the deployed binary version, used for resource attributes.
	ServiceVersion string
}

// Providers holds the initialized SDK providers.
// Stored in the fx container so lifecycle hooks can shut them down cleanly.
type Providers struct {
	Tracer *sdktrace.TracerProvider
	Meter  *metric.MeterProvider
}

// Module registers OpenTelemetry providers with fx lifecycle.
var Module = fx.Module("otel",
	fx.Provide(NewProviders),
)

// NewProviders initialises trace and metric providers and sets them as globals.
func NewProviders(lc fx.Lifecycle, cfg Config, logger *zap.Logger) (*Providers, error) {
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: build resource: %w", err)
	}

	traceExporter, err := otlptracegrpc.New(context.Background(),
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: create trace exporter: %w", err)
	}

	metricExporter, err := otlpmetricgrpc.New(context.Background(),
		otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: create metric exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	mp := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(metricExporter)),
		metric.WithResource(res),
	)

	providers := &Providers{Tracer: tp, Meter: mp}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			otel.SetTracerProvider(tp)
			otel.SetMeterProvider(mp)
			logger.Info("opentelemetry providers registered",
				zap.String("endpoint", cfg.Endpoint),
				zap.String("service", cfg.ServiceName),
			)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if err := tp.Shutdown(ctx); err != nil {
				logger.Warn("otel: trace provider shutdown error", zap.Error(err))
			}
			if err := mp.Shutdown(ctx); err != nil {
				logger.Warn("otel: meter provider shutdown error", zap.Error(err))
			}
			logger.Info("opentelemetry providers shut down")
			return nil
		},
	})

	return providers, nil
}
