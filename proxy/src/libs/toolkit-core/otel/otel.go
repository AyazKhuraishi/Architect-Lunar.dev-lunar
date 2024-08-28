package otel

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkMetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

const (
	prometheusHost = "0.0.0.0:3000"
	metricsRoute   = "/metrics"
	meterName      = "lunar-proxy"
)

// Initializes an OTLP exporter, and configures the corresponding trace and
// metric providers.
func InitProvider(
	ctx context.Context,
	serviceName string,
) func() {
	resource, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithAttributes(
			// the service name used to display traces in backends
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	handleErr(err, "Failed to create resource")

	// The exporter embeds a default OpenTelemetry Reader and
	// implements prometheus.Collector, allowing it to be used as
	// both a Reader and Collector.
	exporter, err := prometheus.New(
		prometheus.WithoutScopeInfo(),
	)
	if err != nil {
		// handleErr(err, "Failed to run exporter embeds")
		log.Error().Err(err).Msg("Failed to run exporter embeds")
	}
	meterProvider := sdkMetric.NewMeterProvider(
		sdkMetric.WithReader(exporter),
	)
	setRealMeter(meterProvider.Meter(meterName))

	var tracerProvider sdktrace.TracerProvider
	otelAgentAddr, traceProviderEnabled := os.LookupEnv(
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")

	if traceProviderEnabled {
		traceClient := otlptracegrpc.NewClient(
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithEndpoint(otelAgentAddr),
			otlptracegrpc.WithDialOption(grpc.WithBlock()))

		traceExporter, err := otlptrace.New(ctx, traceClient)
		handleErr(err, "Failed to create the collector trace exporter")

		bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
		tracerProvider := sdktrace.NewTracerProvider(
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
			sdktrace.WithResource(resource),
			sdktrace.WithSpanProcessor(bsp),
		)

		// set global propagator to trace context (the default is no-op).
		otel.SetTextMapPropagator(propagation.
			NewCompositeTextMapPropagator(propagation.TraceContext{},
				propagation.Baggage{}))
		otel.SetTracerProvider(tracerProvider)
	}

	return func() {
		cxt, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if traceProviderEnabled {
			if err := tracerProvider.Shutdown(cxt); err != nil {
				otel.Handle(err)
			}
		}

		// pushes any last exports to the receiver
		if err := meterProvider.Shutdown(cxt); err != nil {
			otel.Handle(err)
		}
	}
}

func handleErr(err error, message string) {
	if err != nil {
		log.Error().Err(err).Msg(message)
	}
}

func Tracer(ctx context.Context, spanName string) (
	context.Context, trace.Span,
) {
	return otel.Tracer("lunar-engine").Start(ctx, spanName)
}
