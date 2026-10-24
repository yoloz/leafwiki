package pages_test

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/perber/wiki/internal/urlimport"
	wikiassets "github.com/perber/wiki/internal/wiki/assets"
	"github.com/perber/wiki/internal/wiki/pages"
)

// stubImageFetcher records requested URLs and serves a canned PNG payload
// named after the URL path. URLs listed in failing return an error, mimicking
// blocked/hotlink-protected/oversized downloads.
type stubImageFetcher struct {
	mu       sync.Mutex
	requests []string
	failing  map[string]bool
}

func (s *stubImageFetcher) FetchImage(_ context.Context, rawURL string, _ string) (*urlimport.FetchedImage, error) {
	s.mu.Lock()
	s.requests = append(s.requests, rawURL)
	s.mu.Unlock()
	if s.failing[rawURL] {
		return nil, errors.New("blocked")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	return &urlimport.FetchedImage{
		Data:        []byte("\x89PNG\r\n\x1a\n"),
		ContentType: "image/png",
		Filename:    path.Base(u.Path),
	}, nil
}

func newImportURLCreateUC(t *testing.T, deps *testDeps, fetcher *stubImageFetcher) *pages.ImportURLCreateUseCase {
	t.Helper()
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	uploadUC := wikiassets.NewUploadAssetUseCase(deps.tree, deps.assets, deps.revision, slog.Default())
	return pages.NewImportURLCreateUseCase(createUC, updateUC, uploadUC, fetcher, 1024*1024, slog.Default())
}

func TestImportURLCreateUseCase_LocalizesImages(t *testing.T) {
	deps := newTestDeps(t)
	fetcher := &stubImageFetcher{
		failing: map[string]bool{"https://cdn.example.com/blocked.png": true},
	}
	uc := newImportURLCreateUC(t, deps, fetcher)

	content := strings.Join([]string{
		"# Title",
		"",
		"![ok](https://cdn.example.com/ok.png)",
		"",
		"![relative](images/rel.png)",
		"",
		"![blocked](https://cdn.example.com/blocked.png)",
		"",
		"![data](data:image/png;base64,AAAA)",
		"",
		"![local](/assets/somepage/x.png)",
		"",
		"```",
		"![fenced](https://cdn.example.com/fenced.png)",
		"```",
		"",
		"Inline `![code](https://cdn.example.com/code.png)` stays.",
		"",
		"![titled](https://cdn.example.com/titled.png \"a title\")",
	}, "\n")

	out, err := uc.Execute(context.Background(), pages.ImportURLCreateInput{
		UserID:    "user1",
		Title:     "Imported",
		Slug:      "imported",
		Content:   content,
		SourceURL: "https://example.com/blog/post",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Page.Title != "Imported" || out.Page.Slug != "imported" {
		t.Fatalf("unexpected page: title=%q slug=%q", out.Page.Title, out.Page.Slug)
	}

	got := out.Page.Content
	mustContain := []string{
		"![ok](/assets/" + out.Page.ID + "/ok.png)",
		"![relative](/assets/" + out.Page.ID + "/rel.png)",
		// Failed download keeps the original absolute URL.
		"![blocked](https://cdn.example.com/blocked.png)",
		// References that need no download stay untouched.
		"![data](data:image/png;base64,AAAA)",
		"![local](/assets/somepage/x.png)",
		"![fenced](https://cdn.example.com/fenced.png)",
		"`![code](https://cdn.example.com/code.png)`",
		"![titled](https://cdn.example.com/titled.png \"a title\")",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("content missing %q\ngot:\n%s", want, got)
		}
	}

	// The relative reference must have been resolved against the source URL.
	fetcher.mu.Lock()
	requests := append([]string(nil), fetcher.requests...)
	fetcher.mu.Unlock()
	if !containsString(requests, "https://example.com/blog/images/rel.png") {
		t.Errorf("requests = %v, want resolved relative URL", requests)
	}
	for _, unwanted := range []string{
		"images/rel.png",
		"https://cdn.example.com/fenced.png",
		"https://cdn.example.com/code.png",
		"https://cdn.example.com/titled.png",
	} {
		if containsString(requests, unwanted) {
			t.Errorf("fetched %q, want it skipped", unwanted)
		}
	}

	// Both downloaded images are stored as page attachments on disk.
	entries, err := os.ReadDir(filepath.Join(deps.storageDir, "assets", out.Page.ID))
	if err != nil {
		t.Fatalf("reading asset dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 {
		t.Fatalf("asset files = %v, want exactly 2", names)
	}
}

func TestImportURLCreateUseCase_NoImages(t *testing.T) {
	deps := newTestDeps(t)
	fetcher := &stubImageFetcher{}
	uc := newImportURLCreateUC(t, deps, fetcher)

	content := "# Plain\n\nNo images here.\n"
	out, err := uc.Execute(context.Background(), pages.ImportURLCreateInput{
		UserID:    "user1",
		Title:     "Plain",
		Slug:      "plain",
		Content:   content,
		SourceURL: "https://example.com/post",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.Page.Content, "No images here.") {
		t.Errorf("content = %q, want original text", out.Page.Content)
	}
	if len(fetcher.requests) != 0 {
		t.Errorf("fetcher requests = %v, want none", fetcher.requests)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
