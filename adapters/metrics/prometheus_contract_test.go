package metrics

import (
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricCountersAreSeparatedByChain(t *testing.T) {
	reg := prom.NewRegistry()
	m := New("test", reg)
	for _, tc := range []struct {
		name string
		inc  func(string, uint64)
	}{
		{"block_processed_total", m.IncBlocksProcessed},
		{"sink_events_writes_total", m.IncSinkWrites},
		{"sink_events_errors_total", func(id string, n uint64) {
			for i := uint64(0); i < n; i++ {
				m.IncSinkErrors(id)
			}
		}},
		{"reorgs_total", func(id string, n uint64) {
			for i := uint64(0); i < n; i++ {
				m.IncReorgs(id)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.inc("1", 2)
			tc.inc("1", 3)
			tc.inc("10", 7)
			mfs, err := reg.Gather()
			require.NoError(t, err)
			mf := findMetricFamily(t, mfs, "test_"+tc.name)
			assert.Equal(t, float64(5), findMetricByLabel(t, mf, "chain_id", "1").GetCounter().GetValue())
			assert.Equal(t, float64(7), findMetricByLabel(t, mf, "chain_id", "10").GetCounter().GetValue())
		})
	}
}

func TestMetricGaugesReplacePreviousValue(t *testing.T) {
	reg := prom.NewRegistry()
	m := New("test", reg)
	for _, tc := range []struct {
		name string
		set  func(string, uint64)
	}{
		{"block_lag", m.ObservedBlockLag}, {"indexed_block_height", m.SetIndexedHeight}, {"processor_concurrency", m.SetProcessorConcurrency},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.set("1", 100)
			tc.set("1", 2)
			tc.set("10", 20)
			mfs, err := reg.Gather()
			require.NoError(t, err)
			mf := findMetricFamily(t, mfs, "test_"+tc.name)
			assert.Equal(t, float64(2), findMetricByLabel(t, mf, "chain_id", "1").GetGauge().GetValue())
			assert.Equal(t, float64(20), findMetricByLabel(t, mf, "chain_id", "10").GetGauge().GetValue())
		})
	}
}

func TestMetricHistogramsSeparateSuccessAndFailure(t *testing.T) {
	reg := prom.NewRegistry()
	m := New("test", reg)
	for _, tc := range []struct {
		name    string
		observe func(string, time.Duration, bool)
	}{
		{"sink_write_duration_seconds", m.ObservedSinkWriteDuration}, {"block_fetched_duration_seconds", m.ObservedBlockFetchDuration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.observe("1", 250*time.Millisecond, true)
			tc.observe("1", 750*time.Millisecond, true)
			tc.observe("1", 2*time.Second, false)
			mfs, err := reg.Gather()
			require.NoError(t, err)
			mf := findMetricFamily(t, mfs, "test_"+tc.name)
			success := findMetricByLabel(t, mf, "success", "true").GetHistogram()
			assert.Equal(t, uint64(2), success.GetSampleCount())
			assert.Equal(t, float64(1), success.GetSampleSum())
			failure := findMetricByLabel(t, mf, "success", "false").GetHistogram()
			assert.Equal(t, uint64(1), failure.GetSampleCount())
			assert.Equal(t, float64(2), failure.GetSampleSum())
		})
	}
}
