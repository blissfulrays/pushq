// Package metrics exposes aggregate Prometheus metrics. Deliberately no
// per-queue labels: with 10k+ dynamic queues that would explode cardinality —
// per-queue numbers come from the stats API instead.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	reg *prometheus.Registry

	DispatchTotal    *prometheus.CounterVec // code_class: 2xx|4xx|5xx|error
	DispatchErrors   *prometheus.CounterVec // error_kind
	DispatchDuration prometheus.Histogram
	ClaimedTotal     prometheus.Counter
	RetriesTotal     prometheus.Counter
	DeadLettered     prometheus.Counter
	LeasesExpired    prometheus.Counter
	Inflight         prometheus.Gauge
	ActiveQueues     prometheus.Gauge
}

func New() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		reg: reg,
		DispatchTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pushq_dispatches_total", Help: "Task dispatch attempts by response code class.",
		}, []string{"code_class"}),
		DispatchErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pushq_dispatch_errors_total", Help: "Failed dispatch attempts by error kind.",
		}, []string{"error_kind"}),
		DispatchDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "pushq_dispatch_duration_seconds", Help: "Dispatch round-trip latency.",
			Buckets: prometheus.DefBuckets,
		}),
		ClaimedTotal:  prometheus.NewCounter(prometheus.CounterOpts{Name: "pushq_tasks_claimed_total", Help: "Tasks leased by the dispatcher."}),
		RetriesTotal:  prometheus.NewCounter(prometheus.CounterOpts{Name: "pushq_task_retries_total", Help: "Attempts that were rescheduled for retry."}),
		DeadLettered:  prometheus.NewCounter(prometheus.CounterOpts{Name: "pushq_tasks_dead_lettered_total", Help: "Tasks copied to a dead-letter queue."}),
		LeasesExpired: prometheus.NewCounter(prometheus.CounterOpts{Name: "pushq_leases_expired_total", Help: "Leases reclaimed by the sweeper."}),
		Inflight:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "pushq_dispatch_inflight", Help: "Dispatches currently in flight."}),
		ActiveQueues:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "pushq_active_queues", Help: "Queues tracked by the scheduler."}),
	}
	reg.MustRegister(m.DispatchTotal, m.DispatchErrors, m.DispatchDuration, m.ClaimedTotal,
		m.RetriesTotal, m.DeadLettered, m.LeasesExpired, m.Inflight, m.ActiveQueues,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
