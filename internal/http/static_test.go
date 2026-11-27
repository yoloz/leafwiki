package http

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func staticTestFS(t *testing.T) fstest.MapFS {
	t.Helper()
	// The fake .br body need not be real brotli: the handler relays the
	// precompressed bytes as-is and never decodes them.
	return fstest.MapFS{
		"app-abc.js":    {Data: []byte("console.log('app')")},
		"app-abc.js.gz": {Data: gzipBytes(t, []byte("console.log('app')"))},
		"app-abc.js.br": {Data: []byte("BROTLI-BYTES")},
		"img.svg":       {Data: []byte("<svg/>")},
	}
}

func staticTestRouter(t *testing.T, fsys fs.FS) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Match(GetAndHead, "/static/*filepath", newStaticAssetsHandler(fsys))
	return router
}

func TestStaticAssets_ImmutableCacheHeader(t *testing.T) {
	router := staticTestRouter(t, staticTestFS(t))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/app-abc.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != staticCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, staticCacheControl)
	}
	if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding", got)
	}
}

func TestStaticAssets_IdentityWithoutAcceptEncoding(t *testing.T) {
	router := staticTestRouter(t, staticTestFS(t))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/app-abc.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "console.log('app')" {
		t.Errorf("body = %q, want raw content", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want empty", got)
	}
}

func TestStaticAssets_PrecompressedVariantSelected(t *testing.T) {
	tests := []struct {
		name           string
		acceptEncoding string
		wantCoding     string
		wantBody       string
	}{
		{name: "gzip only", acceptEncoding: "gzip", wantCoding: "gzip"},
		{name: "brotli and gzip prefers brotli", acceptEncoding: "gzip, br", wantCoding: "br", wantBody: "BROTLI-BYTES"},
		{name: "brotli only", acceptEncoding: "br", wantCoding: "br", wantBody: "BROTLI-BYTES"},
		{name: "wildcard maps to gzip", acceptEncoding: "*", wantCoding: "gzip"},
		{name: "gzip refused keeps brotli", acceptEncoding: "gzip;q=0, br", wantCoding: "br", wantBody: "BROTLI-BYTES"},
		{name: "both refused serves identity", acceptEncoding: "gzip;q=0", wantCoding: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := staticTestRouter(t, staticTestFS(t))
			req := httptest.NewRequest(http.MethodGet, "/static/app-abc.js", nil)
			req.Header.Set("Accept-Encoding", tt.acceptEncoding)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Encoding"); got != tt.wantCoding {
				t.Errorf("Content-Encoding = %q, want %q", got, tt.wantCoding)
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
			if tt.wantBody == "" && tt.wantCoding == "" && rec.Body.String() != "console.log('app')" {
				t.Errorf("body = %q, want raw content", rec.Body.String())
			}
		})
	}
}

func TestStaticAssets_ServesGzipVariantOnlyWhenEmitted(t *testing.T) {
	// img.svg has no precompressed variants: gzip acceptance must not
	// synthesize one, the body is the raw file.
	router := staticTestRouter(t, staticTestFS(t))
	req := httptest.NewRequest(http.MethodGet, "/static/img.svg", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "<svg/>" {
		t.Errorf("body = %q, want raw svg", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want empty", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Errorf("Content-Type = %q, want image/svg+xml", got)
	}
}

func TestStaticAssets_RejectsVariantArtifactsAndRoot(t *testing.T) {
	router := staticTestRouter(t, staticTestFS(t))
	for _, path := range []string{"/static/app-abc.js.gz", "/static/app-abc.js.br", "/static/"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", path, rec.Code)
		}
	}
}

func TestStaticAssets_WorksUnderBasePath(t *testing.T) {
	// The route is registered on a group with BasePath, so the request URL
	// carries /wiki/static/... while filepath is just /<file>.
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/wiki")
	group.Match(GetAndHead, "/static/*filepath", newStaticAssetsHandler(staticTestFS(t)))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wiki/static/app-abc.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "console.log('app')" {
		t.Errorf("body = %q, want raw content", rec.Body.String())
	}
}

func TestStaticAssets_MissingFile(t *testing.T) {
	router := staticTestRouter(t, staticTestFS(t))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/missing.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestStaticAssets_HeadServesHeadersWithoutBody(t *testing.T) {
	router := staticTestRouter(t, staticTestFS(t))
	req := httptest.NewRequest(http.MethodHead, "/static/app-abc.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD returned a body of %d bytes, want none", rec.Body.Len())
	}
}

// The integration half runs against the real Vite output, when one exists.
func TestStaticAssets_EmbeddedBuild(t *testing.T) {
	if _, err := frontend.Open("dist/index.html"); err != nil {
		t.Skip("requires built frontend (make ui)")
	}
	var sample string
	err := fs.WalkDir(frontend, "dist/static", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && len(sample) == 0 {
			if _, statErr := fs.Stat(frontend, p+".gz"); statErr == nil {
				sample = strings.TrimPrefix(p, "dist/static/")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if sample == "" {
		t.Skip("build output has no precompressed asset")
	}

	router := gin.New()
	router.Match(GetAndHead, "/static/*filepath", newStaticAssetsHandler(mustSub(t, frontend, "dist/static")))

	req := httptest.NewRequest(http.MethodGet, "/static/"+sample, nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" && got != "br" {
		t.Errorf("Content-Encoding = %q, want gzip or br", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != staticCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, staticCacheControl)
	}
	reader, err := variantReader(rec.Header().Get("Content-Encoding"), rec.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Error("decoded body is empty")
	}
}

func mustSub(t *testing.T, fsys fs.FS, dir string) fs.FS {
	t.Helper()
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func variantReader(coding string, body []byte) (io.Reader, error) {
	if coding == "gzip" {
		return gzip.NewReader(bytes.NewReader(body))
	}
	return bytes.NewReader(body), nil
}
