package handler

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// sensitiveQueryParams are redacted from logged request URIs (F33).
var sensitiveQueryParams = []string{
	"token", "code", "state", "client_secret", "secret", "password",
}

// safeLogger wraps the chi request logger to redact sensitive query parameters
// (e.g., channel access tokens and OIDC code/state) from server logs.
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
	return (&middleware.DefaultLogFormatter{Logger: l.Logger, NoColor: true}).NewLogEntry(sanitizeRequestForLog(r))
}

// sanitizeRequestForLog returns a shallow copy of r whose URL/RequestURI have
// sensitive query parameters replaced with "<redacted>".
func sanitizeRequestForLog(r *http.Request) *http.Request {
	clean := new(http.Request)
	*clean = *r
	urlCopy := *r.URL
	clean.URL = &urlCopy
	q := clean.URL.Query()
	changed := false
	for _, p := range sensitiveQueryParams {
		if q.Get(p) != "" {
			q.Set(p, "<redacted>")
			changed = true
		}
	}
	if changed {
		clean.URL.RawQuery = q.Encode()
		clean.RequestURI = clean.URL.RequestURI()
	}
	return clean
}
