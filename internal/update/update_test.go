package update

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubCandidate 构造一条只指向给定测试服务器的候选链路。
func stubCandidate(name string, srv *httptest.Server) candidate {
	return candidate{name: name, client: srv.Client()}
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// stubCandidates 临时把两条候选链都换成给定候选。
func stubCandidates(t *testing.T, cands []candidate) {
	t.Helper()
	origMeta, origDownload := metadataCandidateBuilder, downloadCandidateBuilder
	metadataCandidateBuilder = func() []candidate { return cands }
	downloadCandidateBuilder = func() []candidate { return cands }
	t.Cleanup(func() {
		metadataCandidateBuilder, downloadCandidateBuilder = origMeta, origDownload
	})
}

func TestRequestWithCandidatesFallsBack(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.13.10"}`))
	}))
	defer good.Close()

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	defer bad.Close()

	cands := []candidate{stubCandidate("bad", bad), stubCandidate("good", good)}
	body, err := requestWithCandidates(cands, good.URL)
	if err != nil {
		t.Fatalf("expected success after fallback, got %v", err)
	}
	if !strings.Contains(string(body), "v0.13.10") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestDoGetRejectsNon200WithStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	_, err := doGet(t.Context(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("error should carry status code, got %v", err)
	}
}

func TestDownloadWithCandidatesStreamsToFile(t *testing.T) {
	payload := strings.Repeat("x", 1<<16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dst := dir + "/archive.zip"
	if err := downloadWithCandidates([]candidate{stubCandidate("srv", srv)}, srv.URL, dst); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	got, err := readFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Fatalf("expected %d bytes, got %d", len(payload), len(got))
	}
}

func TestDownloadWithCandidatesAllFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	err := downloadWithCandidates([]candidate{stubCandidate("srv", srv)}, srv.URL, t.TempDir()+"/x.zip")
	if err == nil {
		t.Fatal("expected error when every route fails")
	}
}

func TestGetLatestInfoServesStaleOnRateLimit(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			_, _ = w.Write([]byte(`{"tag_name":"v0.13.10","published_at":"2026-09-19T00:00:00Z"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	defer srv.Close()

	// 用测试服务器替换真实候选链与地址。
	origURL := updateApiUrl
	updateApiUrl = srv.URL
	defer func() { updateApiUrl = origURL }()
	stubCandidates(t, []candidate{stubCandidate("test", srv)})

	latestCacheMu.Lock()
	latestCache, latestCacheAt = nil, time.Time{}
	latestCacheMu.Unlock()

	first, err := GetLatestInfo()
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if first.TagName != "v0.13.10" {
		t.Fatalf("unexpected tag: %s", first.TagName)
	}

	// 令缓存过期, 但保留 stale 结果。
	latestCacheMu.Lock()
	latestCacheAt = time.Now().Add(-2 * latestCacheTTL)
	latestCacheMu.Unlock()

	second, err := GetLatestInfo()
	if err != nil {
		t.Fatalf("rate-limited call should serve stale, got %v", err)
	}
	if second.TagName != "v0.13.10" {
		t.Fatalf("stale result wrong: %s", second.TagName)
	}
}

func TestRepoSlug(t *testing.T) {
	cases := map[string]string{
		"https://github.com/xiaocqaq/octopus.git": "xiaocqaq/octopus",
		"https://github.com/xiaocqaq/octopus/":    "xiaocqaq/octopus",
		"git@github.com:xiaocqaq/octopus.git":     "xiaocqaq/octopus",
	}
	for in, want := range cases {
		if got := repoSlug(in); got != want {
			t.Errorf("repoSlug(%q)=%q want %q", in, got, want)
		}
	}
}