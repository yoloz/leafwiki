package pages

import (
	"context"
	"errors"
	"strings"

	"github.com/perber/wiki/internal/llm"
)

// ErrImportURLInvalid is returned when the provided URL is empty.
var ErrImportURLInvalid = errors.New("pages: url must not be empty")

// ErrLLMNotConfigured is returned when the AI-summarize path is requested but
// the OpenAI-compatible LLM has not been configured.
var ErrLLMNotConfigured = errors.New("pages: llm is not configured")

// ErrSummarizeEmpty is returned when the content to summarize is empty.
var ErrSummarizeEmpty = errors.New("pages: content must not be empty")

// SummarizeInput is the input for SummarizeUseCase.
type SummarizeInput struct {
	UserID  string
	Content string
}

// SummarizeOutput is the result of SummarizeUseCase.
type SummarizeOutput struct {
	Content string // cleaned/summarized Markdown returned by the LLM
}

// SummarizeUseCase asks the configured OpenAI-compatible LLM to clean up and
// (if long) summarize content that was already fetched from a URL, returning
// improved Markdown for manual saving. It does not fetch anything itself.
type SummarizeUseCase struct {
	llm *llm.Client
	cfg llm.Config
}

// NewSummarizeUseCase constructs a SummarizeUseCase.
func NewSummarizeUseCase(client *llm.Client, cfg llm.Config) *SummarizeUseCase {
	return &SummarizeUseCase{llm: client, cfg: cfg}
}

// Execute asks the LLM to clean/summarize the content. If the LLM is not
// configured it returns ErrLLMNotConfigured without making any request; an empty
// content returns ErrSummarizeEmpty.
func (uc *SummarizeUseCase) Execute(ctx context.Context, in SummarizeInput) (*SummarizeOutput, error) {
	if strings.TrimSpace(in.Content) == "" {
		return nil, ErrSummarizeEmpty
	}
	if !uc.cfg.Configured() {
		return nil, ErrLLMNotConfigured
	}
	out, err := uc.llm.SummarizeContent(ctx, in.Content)
	if err != nil {
		return nil, err
	}
	return &SummarizeOutput{Content: out}, nil
}
