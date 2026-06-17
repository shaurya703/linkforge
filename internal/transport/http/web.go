package http

import (
	"bytes"
	_ "embed"
	"net/http"
	"time"
)

// indexHTML is the single self-contained landing page (inline CSS + JS). It is
// embedded into the binary so the service still deploys as one artifact with no
// separate frontend build — in keeping with the project's single-binary ethos.
//
//go:embed web/index.html
var indexHTML []byte

// indexModTime is fixed at build time so the handler can serve a stable
// Last-Modified / If-Modified-Since without reaching for the wall clock per request.
var indexModTime = time.Now()

// Index serves the landing page at GET /. It must be registered explicitly
// because the redirect handler is a catch-all on /{code}; a single page at the
// root avoids any collision with short codes.
func (h *Handlers) Index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, "index.html", indexModTime, bytes.NewReader(indexHTML))
}
