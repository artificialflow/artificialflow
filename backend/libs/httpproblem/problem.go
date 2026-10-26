// Package httpproblem provides the shared RFC 9457 error envelope used by HTTP services.
package httpproblem

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/artificialflow/artificialflow/backend/libs/logger"
)

const ContentType = "application/problem+json"

type Problem struct {
	Type          string `json:"type"`
	Title         string `json:"title"`
	Status        int    `json:"status"`
	Detail        string `json:"detail,omitempty"`
	Instance      string `json:"instance,omitempty"`
	CorrelationID string `json:"correlationId"`
}

// Middleware converts the plain-text errors emitted by net/http, mux, handlers,
// and authentication middleware into the service-wide RFC 9457 representation.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := newRecorder()
		next.ServeHTTP(recorder, r)

		for key, values := range recorder.header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		if isVersionedPath(r.URL.Path) &&
			recorder.status >= http.StatusBadRequest &&
			strings.HasPrefix(recorder.header.Get("Content-Type"), "text/plain") {
			detail := strings.TrimSpace(recorder.body.String())
			correlationID := logger.CorrelationIDFromContext(r.Context())
			if correlationID == "" {
				correlationID = strings.TrimSpace(w.Header().Get("X-Correlation-ID"))
			}
			w.Header().Set("Content-Type", ContentType)
			w.WriteHeader(recorder.status)
			_ = json.NewEncoder(w).Encode(Problem{
				Type:          "about:blank",
				Title:         http.StatusText(recorder.status),
				Status:        recorder.status,
				Detail:        detail,
				Instance:      r.URL.Path,
				CorrelationID: correlationID,
			})
			return
		}

		w.WriteHeader(recorder.status)
		_, _ = w.Write(recorder.body.Bytes())
	})
}

func isVersionedPath(path string) bool {
	return path == "/v1" || strings.HasPrefix(path, "/v1/")
}

type recorder struct {
	header      http.Header
	status      int
	body        bytes.Buffer
	wroteHeader bool
}

func newRecorder() *recorder {
	return &recorder{header: make(http.Header), status: http.StatusOK}
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
}

func (r *recorder) Write(body []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.body.Write(body)
}
