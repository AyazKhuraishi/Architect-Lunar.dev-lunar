package services

import (
	"context"
	"lunar/engine/services/diagnoses"
	"lunar/engine/services/exporters"
	"lunar/engine/services/remedies"
	"lunar/engine/utils/obfuscation"
	"lunar/engine/utils/writers"
	"lunar/toolkit-core/clock"
	"lunar/toolkit-core/otel"
	"time"
)

func InitializeServices(
	clock clock.Clock,
	syslogWriter writers.Writer,
	proxyTimeout time.Duration,
) (*Services, error) {
	md5Obfuscator := obfuscation.Obfuscator{Hasher: obfuscation.MD5Hasher{}}
	identityObfuscator := obfuscation.Obfuscator{
		Hasher: obfuscation.IdentityHasher{},
	}
	ctx := context.Background()
	meter := otel.GetMeter()
	strategyBasedThrottlingPlugin, err := remedies.NewStrategyBasedThrottlingPlugin( //nolint:lll
		ctx,
		clock,
		meter,
		identityObfuscator,
	)
	if err != nil {
		return nil, err
	}

	return &Services{
		Remedies: RemedyPlugins{
			FixedResponsePlugin: remedies.NewFixedResponsePlugin(clock),
			ResponseBasedThrottlingPlugin: remedies.NewResponseBasedThrottlingPlugin(
				clock,
			),
			StrategyBasedThrottlingPlugin: strategyBasedThrottlingPlugin,
			ConcurrencyBasedThrottlingPlugin: remedies.NewConcurrencyBasedThrottlingPlugin( //nolint:lll
				clock,
				proxyTimeout,
			),
			AccountOrchestrationPlugin: remedies.NewAccountOrchestrationPlugin(),
			RetryPlugin:                remedies.NewRetryPlugin(clock),
			AuthPlugin:                 remedies.NewAuthPlugin(),
		},
		Diagnosis: DiagnosisPlugins{
			HARGeneratorPlugin: diagnoses.NewHARGeneratorPlugin(
				clock,
				md5Obfuscator,
			),
			MetricsCollector: &diagnoses.MetricsCollectorPlugin{},
			Void:             &diagnoses.VoidPlugin{},
		},
		Exporters: Exporters{
			Content:    *exporters.NewRawDataExporter(syslogWriter),
			Prometheus: *exporters.NewPrometheusExporter(ctx, meter),
		},
	}, nil
}
