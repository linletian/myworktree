//go:build !viewportdebug

package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestViewportDebugDisabledReturns404(t *testing.T) {
	srv := &Server{}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, "/api/viewport-debug/report", nil)
		rec := httptest.NewRecorder()
		srv.handleViewportDebugReport(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d, want 404 when viewportdebug is not compiled", method, rec.Code)
		}
	}
}
