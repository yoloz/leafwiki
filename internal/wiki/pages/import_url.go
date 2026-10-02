package pages

import (
	"context"
	"errors"
	"fmt"

	"github.com/perber/wiki/internal/urlimport"
)

// ErrImportFailed wraps a URL fetch/extract failure so handlers can return a
// clean, non-leaking message instead of echoing raw network/DNS details.
var ErrImportFailed = errors.New("pages: import failed")

// ImportURLInput is the input for ImportURLUseCase (method 1: readability).
type ImportURLInput struct {
	UserID string
	URL    string
}

// ImportURLOutput is the result of ImportURLUseCase.
type ImportURLOutput struct {
	Title   string
	Content string
	// Format is "markdown" when converted, or "text"/"html" when raw content
	// is returned for manual cleanup.
	Format string
}

// ImportURLUseCase fetches a URL and extracts its article content for manual saving.
type ImportURLUseCase struct {
	importer *urlimport.Importer
}

// NewImportURLUseCase constructs an ImportURLUseCase.
func NewImportURLUseCase(importer *urlimport.Importer) *ImportURLUseCase {
	return &ImportURLUseCase{importer: importer}
}

// Execute fetches and extracts the page. It does NOT create a page: the caller
// reviews the returned content and saves it through the normal create flow.
func (uc *ImportURLUseCase) Execute(ctx context.Context, in ImportURLInput) (*ImportURLOutput, error) {
	if in.URL == "" {
		return nil, ErrImportURLInvalid
	}
	res, err := uc.importer.Extract(ctx, in.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrImportFailed, err)
	}
	return &ImportURLOutput{Title: res.Title, Content: res.Content, Format: res.Format}, nil
}
