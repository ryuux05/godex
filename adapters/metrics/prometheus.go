package metrics

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/ryuux05/godex/pkg/godex"
)

type PrometheusMetrics struct {
	blocksProcessed *prometheus.CounterVec
	blockLag *prometheus.GaugeVec

	sinkWriteDuration *prometheus.HistogramVec
	sinkWrites *prometheus.CounterVec
	sinkErrors *prometheus.CounterVec

	indexedHeight *prometheus.GaugeVec

	blockFetchDuration *prometheus.HistogramVec
	processorConcurrency *prometheus.GaugeVec

	reorgs *prometheus.CounterVec
}

func New(namespace string, reg prometheus.Registerer) *PrometheusMetrics {
	m := &PrometheusMetrics{
		blocksProcessed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name: "block_processed_total",
				Help: "Total number of block processed per chain",
			},
			[]string{"chain_id"},
		),
		blockLag: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name: "block_lag",
				Help: "Head block - last indexed block for each chain.",
			},
			[]string{"chain_id"},
		),
		sinkWriteDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name: "sink_write_duration_seconds",
				Help: "Duration of sink writes (store calls) including COPY/INSERT, handler, and cursor update.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"chain_id", "success"},
		),
		sinkWrites: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name: "sink_events_writes_total",
				Help: "Total number of canonical events succefully written by sink per chain",
			},
			[]string{"chain_id"},
		),
		sinkErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name: "sink_events_errors_total",
				Help: "Total number of failed sink writes per chain",
			},
			[]string{"chain_id"},
		),
		indexedHeight: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name: "indexed_block_height",
				Help: "Last succefully indexed block height per chain.",
			},
			[]string{"chain_id"},
		),
		blockFetchDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name: "block_fetched_duration_seconds",
				Help: "Duration of fetching and decoding a batch of blocks per chain",
			},
			[]string{"chain_id", "success"},
		),
		processorConcurrency: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name: "processor_concurrency",
				Help: "Configured / current processor concurrency per chain.",
			},
			[]string{"chain_id"},
		),
		reorgs: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name: "reorgs_total",
				Help: "Total number of detected / handled reorgs per chain.",
			},
			[]string{"chain_id"},
		),
	}

	reg.MustRegister(
		m.blockFetchDuration,
		m.blockLag,
		m.blocksProcessed,
		m.indexedHeight,
		m.processorConcurrency,
		m.reorgs,
		m.sinkErrors,
		m.sinkWriteDuration,
		m.sinkWrites,
	)

	return m
}

func(m *PrometheusMetrics) IncBlocksProcessed(chainId string, n uint64) {
	m.blocksProcessed.WithLabelValues(chainId).Add(float64(n))
}

func(m *PrometheusMetrics) ObservedBlockLag(chainId string, lag uint64) {
	m.blockLag.WithLabelValues(chainId).Set(float64(lag))
}

func(m *PrometheusMetrics) ObservedBlockFetchDuration(chainId string, d time.Duration, success bool) {
	m.blockFetchDuration.
	WithLabelValues(chainId, fmt.Sprintf("%t", success)).
	Observe(d.Seconds())
}

func(m *PrometheusMetrics) SetIndexedHeight(chainId string, height uint64) {
	m.indexedHeight.WithLabelValues(chainId).Set(float64(height))
}

func(m *PrometheusMetrics) IncSinkWrites(chainId string, n uint64) {
	m.sinkWrites.WithLabelValues(chainId).Add(float64(n))
}

func(m *PrometheusMetrics) SetProcessorConcurrency(chainId string, n uint64) {
	m.processorConcurrency.WithLabelValues(chainId).Set(float64(n))
}

func(m *PrometheusMetrics) IncSinkErrors(chainId string) {
	m.sinkErrors.WithLabelValues(chainId).Inc()
}

func(m *PrometheusMetrics) ObservedSinkWriteDuration(chainId string, d time.Duration, success bool) {
	m.sinkWriteDuration.
	WithLabelValues(chainId, fmt.Sprintf("%t", success)).
	Observe(d.Seconds())
}

func(m *PrometheusMetrics) IncReorgs(chainId string) {
    m.reorgs.WithLabelValues(chainId).Inc()
}

// enforce go at compile time that PrometheusMetrics implements core metrics
var _ godex.Metrics = (*PrometheusMetrics)(nil)
