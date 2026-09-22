package handler

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// safeLogger wraps the chi request logger to redact sensitive query parameters
// (e.g., channel access tokens) from server logs.
func SafeLogger(next http.Handler) http.Handler {
	return middleware.RequestLogger(&safeLogFormatter{Logger: log.Default()})(next)
}

type safeLogFormatter struct {
	Logger *log.Logger
}

func (l *safeLogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	clean := new(http.Request)
	*clean = *r
	q := r.URL.Query()
	if q.Get("token") != "" {
		cleanQ := q
		cleanQ.Set("token", "<redacted>")
		clean.URL.RawQuery = cleanQ.Encode()
	}
	return (&middleware.DefaultLogFormatter{Logger: l.Logger, NoColor: true}).NewLogEntry(clean)
}
