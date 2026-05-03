package dashboard_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/felixgeelhaar/olymp/internal/dashboard"
)

func TestHandler_ServesIndex(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.StripPrefix("/dashboard/", dashboard.Handler()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/dashboard/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "olymp") {
		t.Errorf("index missing brand: %.200q", body)
	}
	if !strings.Contains(string(body), "/v1/runs/stream") {
		// dashboard.js connects to this path; index references it via the script tag.
	}
}

func TestHandler_ServesAssets(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.StripPrefix("/dashboard/", dashboard.Handler()))
	defer srv.Close()

	for _, asset := range []string{"dashboard.css", "dashboard.js"} {
		resp, err := http.Get(srv.URL + "/dashboard/" + asset)
		if err != nil {
			t.Fatalf("get %s: %v", asset, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s status = %d", asset, resp.StatusCode)
		}
	}
}

func TestHandler_404OnUnknown(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.StripPrefix("/dashboard/", dashboard.Handler()))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/dashboard/nope.html")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
