package otel

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// outboxPendingMetricName and fiscalOverdueMetricName are the bare OTel
// instrument names — deliberately WITHOUT an "onlinemenu_" prefix.
//
// The prod otelcol prometheus exporter (deploy/otelcol/config.yaml) sets
// `namespace: onlinemenu`, which the exporter prepends unconditionally as
// "<namespace>_<name>" (verified against opentelemetry-collector-contrib's
// pkg/translator/prometheus BuildCompliantName — it does not check whether
// the name already starts with the namespace). Naming these instruments
// "onlinemenu_outbox_pending" here would therefore surface in Prometheus as
// "onlinemenu_onlinemenu_outbox_pending". Keeping the OTel-side name bare
// makes the collector's namespace prefix the ONLY source of the
// "onlinemenu_" prefix, so Prometheus ends up scraping exactly
// "onlinemenu_outbox_pending" / "onlinemenu_fiscal_submissions_overdue" —
// the names deploy/prometheus/rules.yml alerts on.
const (
	outboxPendingMetricName  = "outbox_pending"
	fiscalOverdueMetricName  = "fiscal_submissions_overdue"
	metricsMeterName         = "onlinemenu"
	outboxPendingModuleLabel = "module"
)

// Metrics exposes platform-level operational gauges backed by atomic
// counters. Components that observe a backlog (the outbox dispatcher, the
// fiscal reconciler) call the Set* methods after each cycle; the gauge
// callbacks below read them lock-free whenever the collector scrapes, so
// there is no push path and no goroutine of its own.
//
// All methods are safe to call on a nil *Metrics (a no-op): callers that
// build their dependencies as plain struct literals in unit tests — bypassing
// fx entirely — leave this field nil, and must not have to guard every call.
type Metrics struct {
	// outboxPending holds one *atomic.Int64 per outbox module, created lazily
	// on first observation (module string -> *atomic.Int64).
	outboxPending sync.Map
	fiscalOverdue atomic.Int64
}

// NewMetrics registers the onlinemenu_outbox_pending (per-module gauge) and
// onlinemenu_fiscal_submissions_overdue observable gauges against the
// "onlinemenu" meter and returns the handle used to feed their values.
func NewMetrics(providers *Providers) (*Metrics, error) {
	m := &Metrics{}
	meter := providers.Meter.Meter(metricsMeterName)

	_, err := meter.Int64ObservableGauge(
		outboxPendingMetricName,
		metric.WithDescription("Unprocessed outbox rows waiting to be dispatched, by module"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			m.outboxPending.Range(func(key, value any) bool {
				o.Observe(value.(*atomic.Int64).Load(),
					metric.WithAttributes(attribute.String(outboxPendingModuleLabel, key.(string))))
				return true
			})
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: create outbox pending gauge: %w", err)
	}

	_, err = meter.Int64ObservableGauge(
		fiscalOverdueMetricName,
		metric.WithDescription("Fiscal submissions past their expected result window"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(m.fiscalOverdue.Load())
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: create fiscal overdue gauge: %w", err)
	}

	return m, nil
}

// SetOutboxPending records module's current unprocessed-row count, read by
// the gauge callback on the next scrape.
func (m *Metrics) SetOutboxPending(module string, count int64) {
	if m == nil {
		return
	}
	v, _ := m.outboxPending.LoadOrStore(module, new(atomic.Int64))
	v.(*atomic.Int64).Store(count)
}

// SetFiscalOverdue records the current count of stale fiscal submissions.
func (m *Metrics) SetFiscalOverdue(count int64) {
	if m == nil {
		return
	}
	m.fiscalOverdue.Store(count)
}
