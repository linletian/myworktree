//go:build !viewportdebug

package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestViewportDebugDisabledAssetsAreNotServed(t *testing.T) {
	mux := http.NewServeMux()
	if err := Register(mux, "myworktree", nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `<script src="/static/viewport_debug.js"></script>`) {
		t.Fatal("stable build must not inject the viewport diagnostic script")
	}

	assetResp, err := http.Get(srv.URL + "/static/viewport_debug.js")
	if err != nil {
		t.Fatal(err)
	}
	_ = assetResp.Body.Close()
	if assetResp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET diagnostic asset: status=%d, want 404", assetResp.StatusCode)
	}
}
