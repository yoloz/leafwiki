// Package urlimport fetches a public web page and extracts its main article
// content. It is deliberately restricted to internet http/https URLs and blocks
// requests that would resolve to private/loopback/link-local addresses (SSRF
// protection). Content extraction uses go-readability; the resulting HTML is
// converted to Markdown, falling back to raw text when conversion is unavailable.
package urlimport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	md "github.com/JohannesKaufmann/html-to-markdown"
)

const maxResponseBytes = 10 << 20 // 10 MiB
const maxImageBytes = 10 << 20    // 10 MiB

// Importer fetches and extracts web page content.
type Importer struct {
	client *http.Client
}

// New constructs an Importer with SSRF-safe transport.
func New() *Importer {
	return &Importer{
		client: &http.Client{
			Timeout: 30 * time.Second,
			// Validate the scheme on every redirect hop.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if err := assertSafeScheme(req.URL); err != nil {
					return err
				}
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
			Transport: &http.Transport{
				// Dial only non-private IPs, regardless of what DNS returns at
				// connect time, to defend against DNS rebinding.
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					host, port, err := net.SplitHostPort(addr)
					if err != nil {
						return nil, err
					}
					ips, err := net.LookupIP(host)
					if err != nil {
						return nil, fmt.Errorf("resolve %q: %w", host, err)
					}
					for _, ip := range ips {
						if isPrivateIP(ip) {
							continue
						}
						d := net.Dialer{Timeout: 10 * time.Second}
						conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
						if err == nil {
							return conn, nil
						}
					}
					return nil, fmt.Errorf("blocked: %q resolves only to private/loopback addresses", host)
				},
			},
		},
	}
}

func assertSafeScheme(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http and https URLs are allowed, got %q", u.Scheme)
	}
	return nil
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	return false
}

// Fetch downloads the page body, enforcing the scheme/SSRF rules above. It
// returns the body, the Content-Type header, and the final URL after redirects.
func (i *Importer) Fetch(ctx context.Context, rawURL string) (body []byte, contentType string, finalURL string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", "", fmt.Errorf("invalid url: %w", err)
	}
	if err := assertSafeScheme(u); err != nil {
		return nil, "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("User-Agent", "leafwiki-url-importer/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.8,*/*;q=0.5")

	resp, err := i.client.Do(req)
	if err != nil {
		return nil, "", "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", "", fmt.Errorf("fetch failed: unexpected status %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxResponseBytes)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", "", fmt.Errorf("read body: %w", err)
	}

	final := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return data, resp.Header.Get("Content-Type"), final, nil
}

// FetchedImage is a downloaded image payload ready to be stored as an asset.
type FetchedImage struct {
	Data        []byte
	ContentType string
	Filename    string
}

// FetchImage downloads a single image, enforcing the same scheme and SSRF rules
// as Fetch. The referer is sent as the Referer header to satisfy sites with
// hotlink protection. Payloads larger than maxImageBytes, empty bodies, and
// non-image content are rejected.
func (i *Importer) FetchImage(ctx context.Context, rawURL string, referer string) (*FetchedImage, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if err := assertSafeScheme(u); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "leafwiki-url-importer/1.0")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/png,image/jpeg,image/gif,image/svg+xml,image/*,*/*;q=0.8")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := i.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch failed: unexpected status %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxImageBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(data)) > maxImageBytes {
		return nil, fmt.Errorf("image exceeds %d bytes", maxImageBytes)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty image body")
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		// Some servers mislabel images as application/octet-stream; fall back
		// to sniffing the magic bytes before giving up.
		contentType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return nil, fmt.Errorf("not an image: content type %q", contentType)
	}

	final := u
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL
	}
	return &FetchedImage{
		Data:        data,
		ContentType: contentType,
		Filename:    imageFilenameFromURL(final, contentType),
	}, nil
}

// imageFilenameFromURL derives a filename from the URL path, appending or
// falling back to an extension derived from the content type.
func imageFilenameFromURL(u *url.URL, contentType string) string {
	name := path.Base(u.Path)
	if name == "." || name == "/" || name == ".." {
		name = ""
	}
	ext := path.Ext(name)
	if name != "" && ext != "" {
		return name
	}
	fallback := "image"
	if name != "" {
		fallback = name
	}
	return fallback + extensionForContentType(contentType)
}

// extensionForContentType maps an image content type to a file extension,
// e.g. "image/svg+xml" -> ".svg". Non-image types fall back to ".img".
func extensionForContentType(contentType string) string {
	subtype := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(subtype, ';'); i >= 0 {
		subtype = subtype[:i]
	}
	if !strings.HasPrefix(subtype, "image/") {
		return ".img"
	}
	subtype = strings.TrimPrefix(subtype, "image/")
	if subtype == "" {
		return ".img"
	}
	if i := strings.IndexByte(subtype, '+'); i >= 0 {
		subtype = subtype[:i]
	}
	return "." + subtype
}

// ExtractResult is the outcome of extracting a page.
type ExtractResult struct {
	Title   string
	Content string
	// Format is "markdown" when the article was converted to Markdown, or
	// "text"/"html" when conversion was not possible and raw content is returned.
	Format string
}

// Extract fetches the URL and returns its main article as Markdown when possible,
// otherwise falls back to the raw text/HTML content for manual cleanup.
func (i *Importer) Extract(ctx context.Context, rawURL string) (*ExtractResult, error) {
	body, contentType, finalURL, err := i.Fetch(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	if !strings.Contains(strings.ToLower(contentType), "html") {
		return &ExtractResult{Title: finalURL, Content: string(body), Format: "text"}, nil
	}

	article, err := readability.FromReader(bytes.NewReader(body), mustURL(finalURL))
	if err != nil || article.Node == nil {
		// Readability could not parse it; return the raw HTML for manual handling.
		return &ExtractResult{Title: finalURL, Content: string(body), Format: "html"}, nil
	}

	title := article.Title()
	conv := md.NewConverter("", true, nil)
	htmlBuf := &bytes.Buffer{}
	if renderErr := article.RenderHTML(htmlBuf); renderErr != nil {
		return &ExtractResult{Title: title, Content: string(body), Format: "html"}, nil
	}
	mdStr, convErr := conv.ConvertString(htmlBuf.String())
	if convErr != nil || strings.TrimSpace(mdStr) == "" {
		// No usable Markdown conversion: fall back to the plain-text content.
		textBuf := &bytes.Buffer{}
		if renderErr := article.RenderText(textBuf); renderErr != nil {
			return &ExtractResult{Title: title, Content: string(body), Format: "html"}, nil
		}
		return &ExtractResult{Title: title, Content: strings.TrimSpace(textBuf.String()), Format: "text"}, nil
	}

	return &ExtractResult{Title: title, Content: strings.TrimSpace(mdStr), Format: "markdown"}, nil
}

// mustURL parses s as a URL, returning nil on failure (FromReader tolerates a
// nil URL, falling back to relative-URL handling).
func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		return nil
	}
	return u
}
