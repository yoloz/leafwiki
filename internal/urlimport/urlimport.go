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
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	md "github.com/JohannesKaufmann/html-to-markdown"
)

const maxResponseBytes = 10 << 20 // 10 MiB

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
