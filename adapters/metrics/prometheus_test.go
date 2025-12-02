package metrics

import (
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// helper: find a metric family by name
func findMetricFamily(t *testing.T, mfs []*dto.MetricFamily, name string) *dto.MetricFamily {
	t.Helper()
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf
		}
	}
	t.Fatalf("metric family %q not found", name)
	return nil
}

// helper: get metric with matching label
func findMetricByLabel(t *testing.T, mf *dto.MetricFamily, labelName, labelValue string) *dto.Metric {
	t.Helper()
	for _, m := range mf.Metric {
		for _, l := range m.Label {
			if l.GetName() == labelName && l.GetValue() == labelValue {
				return m
			}
		}
	}
	t.Fatalf("metric with %s=%q not found in %q", labelName, labelValue, mf.GetName())
	return nil
}

func TestIncBlocksProcessed(t *testing.T) {
	reg := prom.NewRegistry()
	m := New("godex", reg)

	m.IncBlocksProcessed("1", 5)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	mf := findMetricFamily(t, mfs, "godex_block_processed_total")
	metric := findMetricByLabel(t, mf, "chain_id", "1")

	if got := metric.GetCounter().GetValue(); got != 5 {
		t.Fatalf("expected counter=5, got %v", got)
	}
}

func TestObservedBlockFetchDuration(t *testing.T) {
	reg := prom.NewRegistry()
	m := New("godex", reg)

	m.ObservedBlockFetchDuration("1", 2*time.Second, true)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	mf := findMetricFamily(t, mfs, "godex_block_fetched_duration_seconds")
	metric := findMetricByLabel(t, mf, "chain_id", "1")

	// Histogram stores sum and count; we just assert sum in seconds and count==1.
	h := metric.GetHistogram()
	if got := h.GetSampleCount(); got != 1 {
		t.Fatalf("expected count=1, got %v", got)
	}
	if got := h.GetSampleSum(); got < 1.9 || got > 2.1 {
		t.Fatalf("expected sum ~= 2.0, got %v", got)
	}
}

func TestObservedSinkWriteDuration(t *testing.T) {
	reg := prom.NewRegistry()
	m := New("godex", reg)

	m.ObservedSinkWriteDuration("1", 500*time.Millisecond, false)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	mf := findMetricFamily(t, mfs, "godex_sink_write_duration_seconds")
	metric := findMetricByLabel(t, mf, "success", "false")

	h := metric.GetHistogram()
	if got := h.GetSampleCount(); got != 1 {
		t.Fatalf("expected count=1, got %v", got)
	}
}
