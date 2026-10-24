package urlimport

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// pngSignature is enough for http.DetectContentType to classify a payload as
// image/png.
var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// jpegSignature is enough for http.DetectContentType to classify a payload as
// image/jpeg.
var jpegSignature = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")

// TestFetchImage covers the image download path: filename derivation from the
// URL, content-type trust/sniffing, the Referer header, and rejection of
// non-images and error statuses. The Importer is built with the test server's
// client because the SSRF guard deliberately blocks loopback addresses.
func TestFetchImage(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pic.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngSignature)
		case "/noext":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegSignature)
		case "/mislabeled":
			// application/octet-stream must fall back to magic-byte sniffing.
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(pngSignature)
		case "/hotlink":
			if r.Header.Get("Referer") != "https://example.com/post" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngSignature)
		case "/text.txt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("just text, not an image"))
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	// t.Cleanup (not defer) so parallel subtests can still reach the server
	// after the parent test function returns.
	t.Cleanup(srv.Close)

	imp := &Importer{client: srv.Client()}

	t.Run("png keeps filename", func(t *testing.T) {
		t.Parallel()
		img, err := imp.FetchImage(context.Background(), srv.URL+"/pic.png", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if img.Filename != "pic.png" {
			t.Errorf("filename = %q, want %q", img.Filename, "pic.png")
		}
		if string(img.Data) != string(pngSignature) {
			t.Errorf("data mismatch: got %d bytes", len(img.Data))
		}
		if img.ContentType != "image/png" {
			t.Errorf("content type = %q, want %q", img.ContentType, "image/png")
		}
	})

	t.Run("extension-less path falls back to content type", func(t *testing.T) {
		t.Parallel()
		img, err := imp.FetchImage(context.Background(), srv.URL+"/noext", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if img.Filename != "noext.jpeg" {
			t.Errorf("filename = %q, want %q", img.Filename, "noext.jpeg")
		}
	})

	t.Run("mislabeled content type is sniffed", func(t *testing.T) {
		t.Parallel()
		img, err := imp.FetchImage(context.Background(), srv.URL+"/mislabeled", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if img.ContentType != "image/png" {
			t.Errorf("content type = %q, want %q", img.ContentType, "image/png")
		}
		if img.Filename != "mislabeled.png" {
			t.Errorf("filename = %q, want %q", img.Filename, "mislabeled.png")
		}
	})

	t.Run("referer is forwarded", func(t *testing.T) {
		t.Parallel()
		img, err := imp.FetchImage(context.Background(), srv.URL+"/hotlink", "https://example.com/post")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if img.Filename != "hotlink.png" {
			t.Errorf("filename = %q, want %q", img.Filename, "hotlink.png")
		}
	})

	t.Run("non-image is rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := imp.FetchImage(context.Background(), srv.URL+"/text.txt", ""); err == nil {
			t.Fatal("expected error for non-image content, got nil")
		}
	})

	t.Run("error status is rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := imp.FetchImage(context.Background(), srv.URL+"/missing", ""); err == nil {
			t.Fatal("expected error for 404, got nil")
		}
	})
}

func TestImageFilenameFromURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		rawURL      string
		contentType string
		want        string
	}{
		{"https://example.com/a/b.png", "image/png", "b.png"},
		{"https://example.com/pic.webp?v=2", "image/webp", "pic.webp"},
		{"https://example.com/download", "image/jpeg", "download.jpeg"},
		{"https://example.com/", "image/png", "image.png"},
		{"https://example.com/blob", "application/octet-stream", "blob.img"},
		{"https://example.com/x.svg", "image/svg+xml", "x.svg"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.rawURL, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(tc.rawURL)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.rawURL, err)
			}
			if got := imageFilenameFromURL(u, tc.contentType); got != tc.want {
				t.Errorf("imageFilenameFromURL(%q, %q) = %q, want %q", tc.rawURL, tc.contentType, got, tc.want)
			}
		})
	}
}

func TestRewriteLazyImages(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "wechat data-src with placeholder src",
			in:   `<img data-src="https://mmbiz.qpic.cn/a.jpg" src="data:image/svg+xml,placeholder">`,
			want: `<img data-src="https://mmbiz.qpic.cn/a.jpg" src="https://mmbiz.qpic.cn/a.jpg">`,
		},
		{
			name: "no data-src is left untouched",
			in:   `<img src="https://example.com/real.png" alt="x">`,
			want: `<img src="https://example.com/real.png" alt="x">`,
		},
		{
			name: "img with data-src but no src gets one inserted",
			in:   `<img data-src="https://example.com/b.png" alt="y">`,
			want: `<img src="https://example.com/b.png" data-src="https://example.com/b.png" alt="y">`,
		},
		{
			name: "multiple imgs",
			in:   `<p><img data-src="https://x.com/1.jpg" src="ph"></p><img src="https://x.com/2.png">`,
			want: `<p><img data-src="https://x.com/1.jpg" src="https://x.com/1.jpg"></p><img src="https://x.com/2.png">`,
		},
		{
			name: "empty data-src is ignored",
			in:   `<img data-src="" src="https://example.com/ok.png">`,
			want: `<img data-src="" src="https://example.com/ok.png">`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rewriteLazyImages(tc.in); got != tc.want {
				t.Errorf("rewriteLazyImages(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractWeChatContent(t *testing.T) {
	t.Parallel()

	page := `<!doctype html><html><head><meta property="og:title" content="我的文章"><title>我的文章</title></head>` +
		`<body>` +
		`<div id="js_content"><p>正文第一段</p><p>正文第二段</p>` +
		`<img data-src="https://mmbiz.qpic.cn/a.jpg" src="data:image/svg+xml,ph"></div>` +
		`<div id="footer">阅读原文 不喜欢</div>` +
		`</body></html>`

	inner, ok := extractWeChatContent(page)
	if !ok {
		t.Fatal("expected to find js_content")
	}
	if !strings.Contains(inner, "正文第一段") || !strings.Contains(inner, "正文第二段") {
		t.Errorf("js_content missing body text: %q", inner)
	}
	if strings.Contains(inner, "阅读原文") {
		t.Errorf("js_content leaked footer: %q", inner)
	}
	if !strings.Contains(inner, `src="https://mmbiz.qpic.cn/a.jpg"`) {
		t.Errorf("lazy image not promoted: %q", inner)
	}
}

func TestExtractWeChatContentFallbackClass(t *testing.T) {
	t.Parallel()

	page := `<div class="rich_media_content"><p>通过 class 命中的正文</p></div>`
	inner, ok := extractWeChatContent(page)
	if !ok || !strings.Contains(inner, "通过 class 命中的正文") {
		t.Errorf("class fallback failed: ok=%v inner=%q", ok, inner)
	}
}

func TestWeChatTitle(t *testing.T) {
	t.Parallel()

	page := `<html><head><meta property="og:title" content="OG标题"><title>文档标题</title></head></html>`
	if got := weChatTitle(page, "fallback"); got != "OG标题" {
		t.Errorf("weChatTitle = %q, want OG标题", got)
	}

	noOg := `<html><head><title>仅标题</title></head></html>`
	if got := weChatTitle(noOg, "fallback"); got != "仅标题" {
		t.Errorf("weChatTitle (no og) = %q, want 仅标题", got)
	}
}

func TestIsWeChatHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want bool
	}{
		{"mp.weixin.qq.com", true},
		{"weixin.qq.com", true},
		{"example.com", false},
		{"mp.weixin.qq.com.evil.com", false},
	}
	for _, tc := range cases {
		if got := isWeChatHost(tc.host); got != tc.want {
			t.Errorf("isWeChatHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestExtensionForContentType(t *testing.T) {
	t.Parallel()

	cases := []struct{ ct, want string }{
		{"image/png", ".png"},
		{"image/jpeg", ".jpeg"},
		{"image/svg+xml", ".svg"},
		{"image/webp", ".webp"},
		{"IMAGE/PNG", ".png"},
		{"image/png; charset=binary", ".png"},
		{"text/html", ".img"},
		{"", ".img"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.ct, func(t *testing.T) {
			t.Parallel()
			if got := extensionForContentType(tc.ct); got != tc.want {
				t.Errorf("extensionForContentType(%q) = %q, want %q", tc.ct, got, tc.want)
			}
		})
	}
}

// TestIsPrivateIP covers the SSRF guard's address classification. A false
// negative here means the importer would happily connect to an internal
// service, so every private/loopback/link-local range must be rejected.
func TestIsPrivateIP(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ip      string
		private bool
	}{
		{"loopback v4", "127.0.0.1", true},
		{"loopback v6", "::1", true},
		{"private 10/8", "10.0.0.1", true},
		{"private 172.16/12", "172.16.5.4", true},
		{"private 192.168/16", "192.168.1.1", true},
		{"link local v4", "169.254.10.10", true},
		{"link local v6", "fe80::1", true},
		{"unspecified v4", "0.0.0.0", true},
		{"unspecified v6", "::", true},
		{"unique local v6", "fc00::1", true},
		{"public v4", "8.8.8.8", false},
		{"public v4 alt", "1.1.1.1", false},
		{"public v6", "2606:4700:4700::1111", false},
		{"nil ip", "", true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var ip net.IP
			if tc.ip != "" {
				ip = net.ParseIP(tc.ip)
				if ip == nil {
					t.Fatalf("test bug: %q is not a valid IP", tc.ip)
				}
			}
			if got := isPrivateIP(ip); got != tc.private {
				t.Errorf("isPrivateIP(%q) = %v, want %v", tc.ip, got, tc.private)
			}
		})
	}
}

// TestAssertSafeScheme verifies that only http/https are accepted, blocking
// file://, gopher://, ftp:// and empty schemes.
func TestAssertSafeScheme(t *testing.T) {
	t.Parallel()

	ok := []string{"http://example.com", "https://example.com/a?b=c"}
	for _, raw := range ok {
		raw := raw
		t.Run("allowed "+raw, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parse %q: %v", raw, err)
			}
			if err := assertSafeScheme(u); err != nil {
				t.Errorf("assertSafeScheme(%q) = %v, want nil", raw, err)
			}
		})
	}

	blocked := []string{"ftp://example.com", "file:///etc/passwd", "gopher://example.com", "//example.com"}
	for _, raw := range blocked {
		raw := raw
		t.Run("blocked "+raw, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parse %q: %v", raw, err)
			}
			if err := assertSafeScheme(u); err == nil {
				t.Errorf("assertSafeScheme(%q) = nil, want error", raw)
			}
		})
	}
}
