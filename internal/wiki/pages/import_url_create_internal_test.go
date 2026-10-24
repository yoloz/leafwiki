package pages

import (
	"net/url"
	"reflect"
	"testing"
)

func TestMarkdownImageRefs(t *testing.T) {
	t.Parallel()

	content := "![a](https://x.test/a.png)\n" +
		"text ![b](<https://x.test/b.png>) more\n" +
		"```\n![fenced](https://x.test/fenced.png)\n```\n" +
		"~~~md\n![tilde](https://x.test/tilde.png)\n~~~\n" +
		"inline `![code](https://x.test/code.png)` end\n" +
		"unclosed `![after](https://x.test/after.png)\n" +
		"![titled](https://x.test/t.png \"title\")\n" +
		"![empty]()\n" +
		"not an image [link](https://x.test/page)\n"

	refs := markdownImageRefs(content)

	var dests []string
	for _, r := range refs {
		span := content[r.destStart:r.destEnd]
		// Angle-bracketed destinations keep their brackets in the span.
		if span != r.dest && span != "<"+r.dest+">" {
			t.Errorf("dest span %q != parsed dest %q", span, r.dest)
		}
		dests = append(dests, r.dest)
	}

	want := []string{
		"https://x.test/a.png",
		"https://x.test/b.png",
	}
	if !reflect.DeepEqual(dests, want) {
		t.Errorf("dests = %v, want %v", dests, want)
	}
}

func TestResolveImageURL(t *testing.T) {
	t.Parallel()

	base, err := url.Parse("https://example.com/blog/post")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}

	cases := []struct {
		dest   string
		want   string
		wantOK bool
	}{
		{"https://cdn.test/a.png", "https://cdn.test/a.png", true},
		{"images/a.png", "https://example.com/blog/images/a.png", true},
		{"/img/a.png", "https://example.com/img/a.png", true},
		{"//cdn.test/a.png", "https://cdn.test/a.png", true},
		{"data:image/png;base64,AAA", "", false},
		{"#anchor", "", false},
		{"/assets/page/a.png", "", false},
		{"assets/page/a.png", "", false},
		{"mailto:a@b.test", "", false},
		{"", "", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.dest, func(t *testing.T) {
			t.Parallel()
			got, ok := resolveImageURL(tc.dest, base, nil)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("resolveImageURL(%q) = (%q, %v), want (%q, %v)", tc.dest, got, ok, tc.want, tc.wantOK)
			}
		})
	}

	// Without a usable base URL, relative destinations cannot be resolved.
	if _, ok := resolveImageURL("images/a.png", nil, &url.Error{}); ok {
		t.Error("expected relative destination to be skipped without a base URL")
	}
}
