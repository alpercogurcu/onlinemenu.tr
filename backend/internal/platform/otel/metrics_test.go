package otel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// newTestMetrics wires Metrics to a ManualReader so tests can collect the
// callback-observed values synchronously, without a real OTLP exporter.
func newTestMetrics(t *testing.T) (*Metrics, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	providers := &Providers{Meter: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))}

	m, err := NewMetrics(providers)
	require.NoError(t, err)
	return m, reader
}

func findGauge(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.Gauge[int64] {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, met := range sm.Metrics {
			if met.Name == name {
				gauge, ok := met.Data.(metricdata.Gauge[int64])
				require.True(t, ok, "metric %q is not an int64 gauge", name)
				return gauge
			}
		}
	}
	t.Fatalf("metric %q not found among %d scope(s)", name, len(rm.ScopeMetrics))
	return metricdata.Gauge[int64]{}
}

// TestMetrics_OutboxPendingGauge_ReportsPerModuleValues proves the gauge is
// per-module (the "module" attribute) and that a later Set overwrites rather
// than accumulates — the dispatcher calls SetOutboxPending with the current
// backlog count every poll cycle, not a delta.
func TestMetrics_OutboxPendingGauge_ReportsPerModuleValues(t *testing.T) {
	m, reader := newTestMetrics(t)

	m.SetOutboxPending("pos", 3)
	m.SetOutboxPending("payment", 7)
	m.SetOutboxPending("pos", 5) // overwrite, not accumulate

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	gauge := findGauge(t, rm, outboxPendingMetricName)
	got := map[string]int64{}
	for _, dp := range gauge.DataPoints {
		module, ok := dp.Attributes.Value(attribute.Key(outboxPendingModuleLabel))
		require.True(t, ok, "data point missing module attribute")
		got[module.AsString()] = dp.Value
	}
	assert.Equal(t, map[string]int64{"pos": 5, "payment": 7}, got)
}

// TestMetrics_FiscalOverdueGauge_ReportsLatestValue mirrors the reconciler's
// usage: one sweep's overdue count replaces the previous sweep's.
func TestMetrics_FiscalOverdueGauge_ReportsLatestValue(t *testing.T) {
	m, reader := newTestMetrics(t)

	m.SetFiscalOverdue(2)
	m.SetFiscalOverdue(4)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	gauge := findGauge(t, rm, fiscalOverdueMetricName)
	require.Len(t, gauge.DataPoints, 1)
	assert.Equal(t, int64(4), gauge.DataPoints[0].Value)
}

// TestMetrics_NilReceiver_IsANoOp lets the outbox dispatcher and the fiscal
// reconciler call Set* unconditionally even when a test builds them as a bare
// struct literal (bypassing fx, so Metrics is nil) instead of forcing every
// call site to guard with a nil check.
func TestMetrics_NilReceiver_IsANoOp(t *testing.T) {
	var m *Metrics
	assert.NotPanics(t, func() {
		m.SetOutboxPending("pos", 1)
		m.SetFiscalOverdue(1)
	})
}
