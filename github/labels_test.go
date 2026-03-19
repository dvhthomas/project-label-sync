package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEnsureLabelExists_Created(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/repos/owner/repo/labels") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	lm := newTestLabelManager(srv, false)
	err := lm.EnsureLabelExists(context.Background(), "owner/repo", "in-progress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["name"] != "in-progress" {
		t.Errorf("name = %q, want %q", gotBody["name"], "in-progress")
	}
	if gotBody["color"] != "ededed" {
		t.Errorf("color = %q, want %q", gotBody["color"], "ededed")
	}
	if gotBody["description"] == "" {
		t.Error("expected non-empty description")
	}
}

func TestEnsureLabelExists_AlreadyExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"resource":"Label","code":"already_exists","field":"name"}]}`))
	}))
	defer srv.Close()

	lm := newTestLabelManager(srv, false)
	err := lm.EnsureLabelExists(context.Background(), "owner/repo", "in-progress")
	if err != nil {
		t.Fatalf("already_exists should succeed, got: %v", err)
	}
}

func TestEnsureLabelExists_ServerErrorThenSuccess(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	lm := newTestLabelManager(srv, false)
	err := lm.EnsureLabelExists(context.Background(), "owner/repo", "retry-label")
	if err != nil {
		t.Fatalf("expected retry to succeed, got: %v", err)
	}
	if atomic.LoadInt32(&callCount) < 2 {
		t.Errorf("expected at least 2 calls (retry), got %d", callCount)
	}
}

func TestAddLabel_HappyPath(t *testing.T) {
	var gotBody map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/repos/owner/repo/issues/42/labels") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	lm := newTestLabelManager(srv, false)
	err := lm.AddLabel(context.Background(), "owner/repo", 42, "in-progress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	labels := gotBody["labels"]
	if len(labels) != 1 || labels[0] != "in-progress" {
		t.Errorf("labels = %v, want [in-progress]", labels)
	}
}

func TestAddLabel_SpecialCharacters(t *testing.T) {
	var gotBody map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	lm := newTestLabelManager(srv, false)
	err := lm.AddLabel(context.Background(), "owner/repo", 42, "status:In Progress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	labels := gotBody["labels"]
	if len(labels) != 1 || labels[0] != "status:In Progress" {
		t.Errorf("labels = %v, want [status:In Progress]", labels)
	}
}

func TestRemoveLabel_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/repos/owner/repo/issues/42/labels/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	lm := newTestLabelManager(srv, false)
	err := lm.RemoveLabel(context.Background(), "owner/repo", 42, "in-progress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemoveLabel_SpecialCharacters(t *testing.T) {
	var gotRawURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawURL = r.RequestURI
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	lm := newTestLabelManager(srv, false)
	err := lm.RemoveLabel(context.Background(), "owner/repo", 42, "status:In Progress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// url.PathEscape encodes spaces but not colons (colons are valid in paths per RFC 3986).
	if !strings.Contains(gotRawURL, "status:In%20Progress") {
		t.Errorf("request URI = %q, want to contain 'status:In%%20Progress'", gotRawURL)
	}
}

func TestRemoveLabel_AlreadyRemoved404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Label does not exist"}`))
	}))
	defer srv.Close()

	lm := newTestLabelManager(srv, false)
	err := lm.RemoveLabel(context.Background(), "owner/repo", 42, "in-progress")
	if err != nil {
		t.Fatalf("404 should succeed (label already removed), got: %v", err)
	}
}

func TestRemoveLabel_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad request"}`))
	}))
	defer srv.Close()

	lm := newTestLabelManager(srv, false)
	err := lm.RemoveLabel(context.Background(), "owner/repo", 42, "in-progress")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestDryRun_NoHTTPCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("HTTP call should not be made in dry-run mode")
	}))
	defer srv.Close()

	lm := newTestLabelManager(srv, true) // DryRun = true

	ctx := context.Background()

	if err := lm.EnsureLabelExists(ctx, "owner/repo", "test"); err != nil {
		t.Errorf("EnsureLabelExists dry-run error: %v", err)
	}
	if err := lm.AddLabel(ctx, "owner/repo", 42, "test"); err != nil {
		t.Errorf("AddLabel dry-run error: %v", err)
	}
	if err := lm.RemoveLabel(ctx, "owner/repo", 42, "test"); err != nil {
		t.Errorf("RemoveLabel dry-run error: %v", err)
	}
}

func TestCheckLabelsExist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"name":"bug"},{"name":"in-progress"},{"name":"enhancement"}]`))
	}))
	defer srv.Close()

	lm := newTestLabelManager(srv, false)
	existing, missing, err := lm.CheckLabelsExist(context.Background(), "owner/repo", []string{"in-progress", "todo", "done"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(existing) != 1 || existing[0] != "in-progress" {
		t.Errorf("existing = %v, want [in-progress]", existing)
	}
	if len(missing) != 2 {
		t.Errorf("missing = %v, want [todo, done]", missing)
	}
}

func TestAddLabel_WithSpace(t *testing.T) {
	var gotBody map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/repos/owner/repo/issues/42/labels") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	lm := newTestLabelManager(srv, false)
	err := lm.AddLabel(context.Background(), "owner/repo", 42, "in progress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	labels := gotBody["labels"]
	if len(labels) != 1 || labels[0] != "in progress" {
		t.Errorf("labels = %v, want [in progress]", labels)
	}
}

func TestRemoveLabel_WithSpace(t *testing.T) {
	var gotRawURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawURL = r.RequestURI
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	lm := newTestLabelManager(srv, false)
	err := lm.RemoveLabel(context.Background(), "owner/repo", 42, "in progress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotRawURL, "in%20progress") {
		t.Errorf("request URI = %q, want to contain 'in%%20progress'", gotRawURL)
	}
}

// newTestLabelManager creates a LabelManager pointed at the test server.
// It replaces the base URL by using a custom transport.
func newTestLabelManager(srv *httptest.Server, dryRun bool) *LabelManager {
	return &LabelManager{
		HTTPClient: srv.Client(),
		Token:      "test-token",
		DryRun:     dryRun,
		baseURL:    srv.URL, // Use test server URL
	}
}
