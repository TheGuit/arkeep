package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// fakeReleases serves a GitHub "latest release" payload and counts requests.
func fakeReleases(t *testing.T, tag string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": tag})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func getVersion(t *testing.T, h *versionHandler) versionResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Get(rec, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Data versionResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Data
}

func TestVersionHandler_UpdateCheckEnabled(t *testing.T) {
	srv, calls := fakeReleases(t, "v1.2.0")
	h := newVersionHandler("1.0.0", true)
	h.releasesURL = srv.URL

	got := getVersion(t, h)
	if got.LatestVersion != "1.2.0" || !got.UpdateAvailable {
		t.Fatalf("got %+v, want latest 1.2.0 with update available", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("release endpoint called %d times, want 1", calls.Load())
	}
}

func TestVersionHandler_UpdateCheckDisabled(t *testing.T) {
	srv, calls := fakeReleases(t, "v1.2.0")
	h := newVersionHandler("1.0.0", false)
	h.releasesURL = srv.URL

	got := getVersion(t, h)
	if got.ServerVersion != "1.0.0" || got.LatestVersion != "1.0.0" || got.UpdateAvailable {
		t.Fatalf("got %+v, want latest = server version and no update", got)
	}
	if calls.Load() != 0 {
		t.Fatalf("release endpoint called %d times with the update check disabled", calls.Load())
	}
}
