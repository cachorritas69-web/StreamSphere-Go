package common

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type Metrics struct {
	service         string
	requestsTotal   atomic.Uint64
	errorsTotal     atomic.Uint64
	durationNanoSum atomic.Uint64
}

func NewMetrics(service string) *Metrics {
	return &Metrics{service: service}
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		m.requestsTotal.Add(1)
		m.durationNanoSum.Add(uint64(time.Since(started)))
		if recorder.status >= 500 {
			m.errorsTotal.Add(1)
		}
	})
}

func (m *Metrics) Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	requests := m.requestsTotal.Load()
	durationSeconds := float64(m.durationNanoSum.Load()) / float64(time.Second)
	_, _ = fmt.Fprintf(w, "# HELP streamsphere_http_requests_total Total de solicitudes HTTP.\n")
	_, _ = fmt.Fprintf(w, "# TYPE streamsphere_http_requests_total counter\n")
	_, _ = fmt.Fprintf(w, "streamsphere_http_requests_total{service=%q} %d\n", m.service, requests)
	_, _ = fmt.Fprintf(w, "# HELP streamsphere_http_errors_total Total de respuestas 5xx.\n")
	_, _ = fmt.Fprintf(w, "# TYPE streamsphere_http_errors_total counter\n")
	_, _ = fmt.Fprintf(w, "streamsphere_http_errors_total{service=%q} %d\n", m.service, m.errorsTotal.Load())
	_, _ = fmt.Fprintf(w, "# HELP streamsphere_http_request_duration_seconds_sum Tiempo acumulado de solicitudes.\n")
	_, _ = fmt.Fprintf(w, "# TYPE streamsphere_http_request_duration_seconds_sum counter\n")
	_, _ = fmt.Fprintf(w, "streamsphere_http_request_duration_seconds_sum{service=%q} %.6f\n", m.service, durationSeconds)
	_, _ = fmt.Fprintf(w, "streamsphere_http_request_duration_seconds_count{service=%q} %d\n", m.service, requests)
}
