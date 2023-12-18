package remedies

import (
	"context"
	"lunar/engine/actions"
	"lunar/engine/config"
	"lunar/engine/messages"
	"lunar/engine/utils/queue"
	sharedConfig "lunar/shared-model/config"
	"lunar/toolkit-core/clock"
	"lunar/toolkit-core/logging"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type queueKey struct {
	remedyName string
	strategy   queue.Strategy
}

type StrategyBasedQueuePlugin struct {
	clock       clock.Clock
	queuesMutex sync.RWMutex
	ctx         context.Context
	queues      map[queueKey]*queue.DelayedPriorityQueue
	metrics     strategyBasedQueueMetrics
	cl          logging.ContextLogger
}

const (
	requestsInQueueMetricName = "lunar_remedies.strategy_based_queue.requests_in_queue" //nolint:lll
	requestsMetricName        = "lunar_remedies.strategy_based_queue.requests"
	// deepcode ignore HardcodedPassword: <This is not a password>
	ttlPassedAttribute = "ttl_passed"
	remedyAttribute    = "remedy"
	priorityAttribute  = "priority"
)

type strategyBasedQueueMetrics struct {
	requestsInQueue metric.Int64ObservableGauge
	requests        metric.Int64Counter
}

func NewStrategyBasedQueuePlugin(
	ctx context.Context,
	clock clock.Clock,
	contextLogger logging.ContextLogger,
	meter metric.Meter,
) *StrategyBasedQueuePlugin {
	plugin := &StrategyBasedQueuePlugin{ //nolint:exhaustruct
		clock:       clock,
		queuesMutex: sync.RWMutex{},
		queues:      map[queueKey]*queue.DelayedPriorityQueue{},
		ctx:         ctx,
		cl:          contextLogger.WithComponent("strategy-based-queue"),
	}
	plugin.metrics.requestsInQueue = plugin.initializeRequestsInQueueMetric(
		meter,
	)
	plugin.metrics.requests = plugin.initializeRequestsMetric(meter)
	return plugin
}

func (plugin *StrategyBasedQueuePlugin) OnRequest(
	onRequest messages.OnRequest,
	scopedRemedy config.ScopedRemedy,
) (actions.ReqLunarAction, error) {
	remedyConfig := scopedRemedy.Remedy.Config.StrategyBasedQueue
	if remedyConfig == nil {
		plugin.cl.Logger.Error().
			Err(ErrMissingConfig).
			Msg("Remedy config missing")
		return &actions.NoOpAction{}, ErrMissingConfig
	}

	strategy := queue.Strategy{
		WindowQuota: remedyConfig.AllowedRequestCount,
		WindowSize: time.Duration(
			remedyConfig.WindowSizeInSeconds,
		) * time.Second,
	}

	queueKey := queueKey{
		remedyName: scopedRemedy.Remedy.Name,
		strategy:   strategy,
	}
	plugin.queuesMutex.Lock()
	relevantQueue, found := plugin.queues[queueKey]
	if !found {
		relevantQueue = queue.NewDelayedPriorityQueue(
			strategy,
			plugin.clock,
			plugin.cl,
		)
		plugin.cl.Logger.Trace().
			Msgf("Initialized delayed prioritized queue for %s (%+v)",
				scopedRemedy.Remedy.Name, strategy)
		plugin.queues[queueKey] = relevantQueue
	}
	plugin.queuesMutex.Unlock()

	priority := extractPriority(onRequest, *remedyConfig)
	plugin.cl.Logger.Trace().Str("requestID", onRequest.ID).
		Msgf("extracted priority %d", priority)

	request := queue.NewRequest(onRequest.ID, priority, plugin.clock)
	canProceed := relevantQueue.Enqueue(
		request,
		time.Duration(remedyConfig.TTLSeconds)*time.Second,
	)
	plugin.cl.Logger.Trace().
		Str("requestID", onRequest.ID).
		Msgf("can proceed response: %v", canProceed)

	if canProceed {
		plugin.incrementRequestsMetric(
			scopedRemedy.Remedy.Name,
			priority,
			false,
		)
		return &actions.NoOpAction{}, nil
	}
	plugin.incrementRequestsMetric(scopedRemedy.Remedy.Name, priority, true)

	plugin.cl.Logger.Trace().Str("requestID", onRequest.ID).
		Msgf("request cannot be processed, will return early response")
	action := plainTextTooManyRequestsAction(
		remedyConfig.ResponseStatusCode,
	)
	return &action, nil
}

// If priority is not defined/find, it will default to 0,
// which is the highest priority.
func extractPriority(
	onRequest messages.OnRequest,
	remedyConfig sharedConfig.StrategyBasedQueueConfig,
) int {
	if remedyConfig.Prioritization == nil {
		return 0
	}
	headerName := remedyConfig.Prioritization.GroupBy.HeaderName
	headerValue := onRequest.Headers[headerName]
	prioritization := remedyConfig.Prioritization.Groups[headerValue]

	return prioritization.Priority
}

func (plugin *StrategyBasedQueuePlugin) OnResponse(
	_ messages.OnResponse,
	_ config.ScopedRemedy,
) (actions.RespLunarAction, error) {
	return &actions.NoOpAction{}, nil
}

func (plugin *StrategyBasedQueuePlugin) initializeRequestsInQueueMetric(
	meter metric.Meter,
) metric.Int64ObservableGauge {
	gauge, err := meter.Int64ObservableGauge(
		requestsInQueueMetricName,
		metric.WithDescription("Current number of requests in queue"),
		metric.WithInt64Callback(plugin.observeRequestsInQueue),
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create requests in queue metric")
	}
	return gauge
}

func (plugin *StrategyBasedQueuePlugin) initializeRequestsMetric(
	meter metric.Meter,
) metric.Int64Counter {
	counter, _ := meter.Int64Counter(requestsMetricName)
	return counter
}

func (plugin *StrategyBasedQueuePlugin) observeRequestsInQueue(
	_ context.Context,
	observer metric.Int64Observer,
) error {
	plugin.queuesMutex.RLock()
	defer plugin.queuesMutex.RUnlock()

	for queueKey, q := range plugin.queues {
		for priority, count := range q.Counts() {
			observer.Observe(
				int64(count),
				metric.WithAttributes(
					attribute.String(remedyAttribute, queueKey.remedyName),
					attribute.Int(priorityAttribute, priority),
				),
			)
		}
	}
	return nil
}

func (plugin *StrategyBasedQueuePlugin) incrementRequestsMetric(
	remedyName string,
	priority int,
	ttlPassed bool,
) {
	plugin.metrics.requests.Add(
		plugin.ctx,
		1,
		metric.WithAttributes(
			attribute.Bool(ttlPassedAttribute, ttlPassed),
			attribute.String(remedyAttribute, remedyName),
			attribute.Int(priorityAttribute, priority),
		),
	)
}
