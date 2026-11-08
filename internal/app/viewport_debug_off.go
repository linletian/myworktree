//go:build !viewportdebug

package app

import "net/http"

// viewportDebugStore is an empty placeholder for stable builds. It keeps the
// Server shape identical without allocating sessions or retaining reports.
type viewportDebugStore struct{}

func newViewportDebugStore() *viewportDebugStore { return &viewportDebugStore{} }

func (s *Server) handleViewportDebugReport(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}
