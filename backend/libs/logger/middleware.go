package logger

import (
	"context"
	"net/http"
	"time"
)

// HTTPMiddleware provides logging middleware for HTTP handlers
type HTTPMiddleware struct {
	log *Logger
}

// NewHTTPMiddleware creates a new HTTP logging middleware
func NewHTTPMiddleware(component string) *HTTPMiddleware {
	return &HTTPMiddleware{
		log: New(component),
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	size, err := rw.ResponseWriter.Write(b)
	rw.size += size
	return size, err
}

// Middleware returns an HTTP middleware that logs requests
func (m *HTTPMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Generate correlation ID from header or create new one
		correlationID := r.Header.Get("X-Correlation-ID")
		if correlationID == "" {
			correlationID = GenerateCorrelationID()
		}

		// Add correlation ID to response header
		w.Header().Set("X-Correlation-ID", correlationID)

		// Create context with correlation ID
		ctx := ContextWithCorrelationID(r.Context(), correlationID)
		r = r.WithContext(ctx)

		// Wrap response writer to capture status code
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Successful requests are high-cardinality hot-path telemetry. Keep
		// their details available at debug level and use metrics for aggregate
		// latency/throughput; warning and error responses remain prominent.
		m.log.Debug(ctx, "request started", map[string]any{
			"method":      r.Method,
			"path":        r.URL.Path,
			"query":       r.URL.RawQuery,
			"remote_addr": r.RemoteAddr,
			"user_agent":  r.UserAgent(),
		})

		// Call next handler
		next.ServeHTTP(wrapped, r)

		// Log request completion
		duration := time.Since(start)
		level := LevelInfo
		if wrapped.statusCode >= 400 {
			level = LevelWarn
		}
		if wrapped.statusCode >= 500 {
			level = LevelError
		}

		fields := map[string]any{
			"method":      r.Method,
			"path":        r.URL.Path,
			"status":      wrapped.statusCode,
			"duration_ms": duration.Milliseconds(),
			"size":        wrapped.size,
		}

		switch level {
		case LevelError:
			m.log.Error(ctx, "request completed", fields)
		case LevelWarn:
			m.log.Warn(ctx, "request completed", fields)
		default:
			m.log.Debug(ctx, "request completed", fields)
		}
	})
}

// ContextFromRequest extracts or creates a context with correlation ID from an HTTP request
func ContextFromRequest(r *http.Request) context.Context {
	correlationID := r.Header.Get("X-Correlation-ID")
	if correlationID == "" {
		correlationID = GenerateCorrelationID()
	}
	return ContextWithCorrelationID(r.Context(), correlationID)
}
