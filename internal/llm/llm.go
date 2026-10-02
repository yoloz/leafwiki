// Package llm provides a minimal OpenAI-compatible chat client used by the
// "AI summarize imported content" feature. It deliberately avoids pulling in a
// heavy SDK: the only endpoint we need is POST {BaseURL}/chat/completions.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config holds the OpenAI-compatible LLM connection settings.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Configured reports whether all three fields needed to talk to the LLM are set.
func (c Config) Configured() bool {
	return strings.TrimSpace(c.BaseURL) != "" &&
		strings.TrimSpace(c.APIKey) != "" &&
		strings.TrimSpace(c.Model) != ""
}

// ErrNotConfigured is returned when a request is made but the LLM is not configured.
var ErrNotConfigured = errors.New("llm: not configured")

// Client talks to an OpenAI-compatible chat completions endpoint.
type Client struct {
	cfg    Config
	client *http.Client
}

// NewClient builds a Client for the given config. A nil/empty config is allowed;
// calls will fail with ErrNotConfigured until Configured() is true.
func NewClient(cfg Config) *Client {
	return &Client{
		cfg: cfg,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// systemPrompt instructs the model to clean up and summarize raw Markdown/text
// that was already extracted from a web page (the server does the fetching; the
// LLM only improves the result).
const systemPrompt = `You are given raw Markdown/text that was extracted from a web page.

Clean it up and turn it into clean, well-structured Markdown. Fix headings, lists, tables, code blocks, links, images and emphasis. Remove any residual navigation menus, advertisements, cookie banners, footers, boilerplate and duplicate content.

If the source is long, summarize the key points concisely while preserving the important details and overall structure.

If the provided content is empty or unusable, respond with exactly:
UNUSABLE_CONTENT

Do not invent, guess, or fabricate any content that is not present in the input.

Respond with Markdown only. Do not add explanations, commentary, JSON, or code fences around the entire output.`

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// SummarizeContent asks the LLM to clean up and (if long) summarize the given
// extracted content, returning improved Markdown. The server is responsible for
// fetching the original page; the LLM only processes what it is given. If the
// LLM is not configured it returns ErrNotConfigured without making a request.
func (c *Client) SummarizeContent(ctx context.Context, content string) (string, error) {
	if !c.cfg.Configured() {
		return "", ErrNotConfigured
	}

	reqBody := chatRequest{
		Model: c.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: strings.TrimSpace(content)},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("llm: marshal request: %w", err)
	}

	endpoint := strings.TrimRight(strings.TrimSpace(c.cfg.BaseURL), "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.cfg.APIKey))

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return "", fmt.Errorf("llm: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("llm: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("llm: api error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("llm: empty response from API")
	}

	out := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if out == "UNUSABLE_CONTENT" {
		return "", errors.New("llm: provided content was empty or unusable")
	}
	return out, nil
}
