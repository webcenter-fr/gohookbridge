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
	// The redaction below must never touch the live request: chi logs
	// r.RequestURI, while downstream middleware (e.g. ChannelAccessMiddleware)
	// parses r.URL.Query(), so both fields are rewritten on a detached copy.
	clean := new(http.Request)
	*clean = *r
	urlCopy := *r.URL
	clean.URL = &urlCopy
	if q := clean.URL.Query(); q.Get("token") != "" {
		q.Set("token", "<redacted>")
		clean.URL.RawQuery = q.Encode()
		clean.RequestURI = clean.URL.RequestURI()
	}
	return (&middleware.DefaultLogFormatter{Logger: l.Logger, NoColor: true}).NewLogEntry(clean)
}
