package http

import (
	"bytes"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// staticCacheControl is sent for every embedded frontend asset. Vite hashes
// the content into the file name (static/[name]-[hash].js), so a URL always
// maps to the same bytes and can be cached forever. First visits still
// download everything, but repeat visits skip the static byte stream
// entirely: on one HTTP/2 connection every asset shares the connection-level
// flow-control window, so dropping repeat downloads is the most effective
// first-paint fix available to a Go server — the send-side windows are
// advertised by the client and cannot be tuned server-side.
const staticCacheControl = "public, max-age=31536000, immutable"

// newStaticAssetsHandler serves the embedded frontend build output under
// /static, replacing gin's StaticFS.
//
// Bodies are served precompressed when the build emitted a .gz/.br variant
// (the precompress-assets Vite plugin) and the client accepts that coding,
// preferring brotli. The shell (index.html) is served by shellHandler instead
// and stays no-cache; the nosniff exemption for /static in
// middleware/security/headers.go must keep matching this prefix.
func newStaticAssetsHandler(fsys fs.FS) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Cache-Control", staticCacheControl)
		c.Writer.Header().Set("Vary", "Accept-Encoding")

		name := strings.TrimPrefix(c.Param("filepath"), "/")
		// The root itself and the precompressed artifacts are not resources:
		// the artifacts exist only as variants picked below, and serving the
		// root would render a directory listing under a permanent cache
		// header. gin's catch-all parameter is URL-decoded, so ".." may
		// appear here; fs.ReadFile rejects such paths with a 404 below.
		if name == "" || strings.HasSuffix(name, ".gz") || strings.HasSuffix(name, ".br") {
			c.Status(http.StatusNotFound)
			return
		}
		if coding, content, ok := pickPrecompressed(fsys, name, acceptEncodingSet(c.GetHeader("Accept-Encoding"))); ok {
			c.Writer.Header().Set("Content-Encoding", coding)
			http.ServeContent(c.Writer, c.Request, name, time.Time{}, bytes.NewReader(content))
			return
		}
		// Read through the injected FS rather than an http.FileServer rooted
		// at a URL prefix: the route lives under opts.BasePath, so the
		// request path may be /wiki/static/... while name is just the file
		// name. ServeContent keeps Range/HEAD handling; embed.FS files are
		// already in memory, so reading them costs no extra I/O.
		content, err := fs.ReadFile(fsys, name)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		http.ServeContent(c.Writer, c.Request, name, time.Time{}, bytes.NewReader(content))
	}
}

// pickPrecompressed returns the best precompressed body for name: brotli
// when available and accepted, else gzip. It only reports a variant when the
// build emitted it.
func pickPrecompressed(fsys fs.FS, name string, accepted map[string]bool) (coding string, content []byte, ok bool) {
	for _, variant := range []struct{ coding, suffix string }{
		{"br", ".br"},
		{"gzip", ".gz"},
	} {
		if !accepted[variant.coding] {
			continue
		}
		content, err := fs.ReadFile(fsys, name+variant.suffix)
		if err != nil {
			continue
		}
		return variant.coding, content, true
	}
	return "", nil, false
}

// acceptEncodingSet parses an Accept-Encoding header into the set of codings
// the client accepts. q=0 refuses a coding. The "*" wildcard maps to gzip
// only: clients that speak brotli always list it explicitly, while "*" alone
// (rare outside of tools) keeps the widely available coding.
func acceptEncodingSet(header string) map[string]bool {
	accepted := make(map[string]bool)
	for _, part := range strings.Split(header, ",") {
		token, params, _ := strings.Cut(part, ";")
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" {
			continue
		}
		q := 1.0
		params = strings.TrimSpace(params)
		if strings.HasPrefix(params, "q=") {
			if value, err := strconv.ParseFloat(strings.TrimSpace(params[2:]), 64); err == nil {
				q = value
			}
		}
		if q <= 0 {
			continue
		}
		if token == "*" {
			accepted["gzip"] = true
			continue
		}
		accepted[token] = true
	}
	return accepted
}
