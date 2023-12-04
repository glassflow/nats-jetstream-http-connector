package metrics

import "github.com/prometheus/client_golang/prometheus"

type CounterV1Func func(string)

func CounterV1(h *prometheus.CounterVec) CounterV1Func {
	return func(v1 string) { h.WithLabelValues(v1).Inc() }
}

func CounterV2(h *prometheus.CounterVec) func(_, _ string) {
	return func(v1, v2 string) { h.WithLabelValues(v1, v2).Inc() }
}

func CounterV3(h *prometheus.CounterVec) func(_, _, _ string) {
	return func(v1, v2, v3 string) { h.WithLabelValues(v1, v2, v3).Inc() }
}

func HistogramV1(h *prometheus.HistogramVec) func(_ string, _ float64) {
	return func(v1 string, value float64) { h.WithLabelValues(v1).Observe(value) }
}

func HistogramV2(h *prometheus.HistogramVec) func(_, _ string, _ float64) {
	return func(v1, v2 string, value float64) { h.WithLabelValues(v1, v2).Observe(value) }
}

func HistogramV3(h *prometheus.HistogramVec) func(_, _, _ string, _ float64) {
	return func(v1, v2, v3 string, value float64) { h.WithLabelValues(v1, v2, v3).Observe(value) }
}
