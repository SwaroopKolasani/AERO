package server

import (
	"log"
	"net/http"
	"time"

	"github.com/swaroop/aero/aerocore/internal/trace"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func newStatusRecorder(w http.ResponseWriter) *statusRecorder {
	return &statusRecorder{
		ResponseWriter: w,
		status:         http.StatusOK,
	}
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	n, err := r.ResponseWriter.Write(body)
	r.bytes += n
	return n, err
}

func (s *Server) serveWithAccessLog(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := newStatusRecorder(w)

	requestID := trace.NormalizeRequestID(r.Header.Get(trace.IncomingRequestIDHeader))
	if requestID == "" {
		requestID = trace.NewRequestID()
	}

	setCoreRequestID(rec, requestID)
	s.mux.ServeHTTP(rec, r)

	log.Printf(
		"aerocore_access request_id=%s method=%s path=%s status=%d bytes=%d duration_ms=%d remote=%s",
		currentCoreRequestID(rec),
		r.Method,
		r.URL.Path,
		rec.status,
		rec.bytes,
		time.Since(start).Milliseconds(),
		r.RemoteAddr,
	)
}
