package llm

import (
	"context"
	"errors"
	"testing"
)

// TestConfigConfigured ensures every field the client needs is required, and
// that whitespace-only values do not count as configured.
func TestConfigConfigured(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"all set", Config{BaseURL: "https://api.example.com/v1", APIKey: "k", Model: "m"}, true},
		{"missing base url", Config{APIKey: "k", Model: "m"}, false},
		{"missing api key", Config{BaseURL: "https://api.example.com/v1", Model: "m"}, false},
		{"missing model", Config{BaseURL: "https://api.example.com/v1", APIKey: "k"}, false},
		{"whitespace only", Config{BaseURL: "  ", APIKey: " ", Model: "\t"}, false},
		{"empty", Config{}, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.cfg.Configured(); got != tc.want {
				t.Errorf("Configured() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSummarizeContentNotConfigured verifies the unconfigured LLM refuses to run
// without attempting any network call — the feature must be a no-op when no LLM
// is configured.
func TestSummarizeContentNotConfigured(t *testing.T) {
	t.Parallel()

	c := NewClient(Config{})
	_, err := c.SummarizeContent(context.Background(), "some content")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("SummarizeContent() error = %v, want ErrNotConfigured", err)
	}
}
