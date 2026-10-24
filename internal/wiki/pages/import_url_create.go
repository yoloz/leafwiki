package pages

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync"

	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/urlimport"
	wikiassets "github.com/perber/wiki/internal/wiki/assets"
)

// importImageConcurrency bounds parallel image downloads so a page with many
// images does not open dozens of connections at once.
const importImageConcurrency = 4

// imageFetcher downloads a single remote image (SSRF-guarded).
type imageFetcher interface {
	FetchImage(ctx context.Context, rawURL string, referer string) (*urlimport.FetchedImage, error)
}

// assetUploader stores an image payload as a page attachment.
type assetUploader interface {
	Execute(ctx context.Context, in wikiassets.UploadAssetInput) (*wikiassets.UploadAssetOutput, error)
}

// ImportURLCreateInput is the input for ImportURLCreateUseCase.
type ImportURLCreateInput struct {
	UserID   string
	ParentID *string
	Title    string
	Slug     string
	// Content is the reviewed Markdown to store. Image references are
	// localized to page attachments before saving.
	Content string
	// SourceURL is the page the content was imported from. Relative image
	// references are resolved against it, and it is sent as the Referer
	// header when downloading images.
	SourceURL string
}

// ImportURLCreateOutput is the result of ImportURLCreateUseCase.
type ImportURLCreateOutput struct {
	Page *tree.Page
}

// ImportURLCreateUseCase creates a page from reviewed import content and
// downloads the images it references as local attachments. Images that fail
// to download keep their original URL in the stored content so the author can
// handle them manually.
type ImportURLCreateUseCase struct {
	createPage    *CreatePageUseCase
	updatePage    *UpdatePageUseCase
	uploadAsset   assetUploader
	images        imageFetcher
	assetMaxBytes int64
	log           *slog.Logger
}

// NewImportURLCreateUseCase constructs an ImportURLCreateUseCase. The
// uploadAsset and images parameters are interfaces so tests can stub them.
func NewImportURLCreateUseCase(
	createPage *CreatePageUseCase,
	updatePage *UpdatePageUseCase,
	uploadAsset assetUploader,
	images imageFetcher,
	assetMaxBytes int64,
	log *slog.Logger,
) *ImportURLCreateUseCase {
	return &ImportURLCreateUseCase{
		createPage:    createPage,
		updatePage:    updatePage,
		uploadAsset:   uploadAsset,
		images:        images,
		assetMaxBytes: assetMaxBytes,
		log:           log,
	}
}

// Execute creates the page, localizes referenced images, and stores the content.
func (uc *ImportURLCreateUseCase) Execute(ctx context.Context, in ImportURLCreateInput) (*ImportURLCreateOutput, error) {
	kind := tree.NodeKindPage
	created, err := uc.createPage.Execute(ctx, CreatePageInput{
		UserID: in.UserID, ParentID: in.ParentID, Title: in.Title, Slug: in.Slug, Kind: &kind,
	})
	if err != nil {
		return nil, err
	}

	content := uc.localizeImages(ctx, in.UserID, created.Page.ID, in.Content, in.SourceURL)

	updated, err := uc.updatePage.Execute(ctx, UpdatePageInput{
		UserID: in.UserID, ID: created.Page.ID, Version: created.Page.Version(),
		Title: in.Title, Slug: in.Slug, Content: &content,
	})
	if err != nil {
		return nil, err
	}
	return &ImportURLCreateOutput{Page: updated.Page}, nil
}

// localizeImages downloads every remote image referenced by the Markdown and
// rewrites successful references to the stored attachment URL. References that
// cannot be downloaded (blocked, too large, not an image, ...) are left as-is.
func (uc *ImportURLCreateUseCase) localizeImages(ctx context.Context, userID, pageID, content, sourceURL string) string {
	refs := markdownImageRefs(content)
	if len(refs) == 0 {
		return content
	}

	base, baseErr := url.Parse(sourceURL)
	rewritten := make([]string, len(refs))

	var wg sync.WaitGroup
	sem := make(chan struct{}, importImageConcurrency)
	for idx, ref := range refs {
		abs, ok := resolveImageURL(ref.dest, base, baseErr)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(idx int, abs string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			img, err := uc.images.FetchImage(ctx, abs, sourceURL)
			if err != nil {
				uc.log.Warn("import: failed to download image", "url", abs, "pageID", pageID, "error", err)
				return
			}
			out, err := uc.uploadAsset.Execute(ctx, wikiassets.UploadAssetInput{
				UserID: userID, PageID: pageID,
				File: byteFile{bytes.NewReader(img.Data)}, Filename: img.Filename,
				MaxBytes: uc.assetMaxBytes,
			})
			if err != nil {
				uc.log.Warn("import: failed to store image", "url", abs, "pageID", pageID, "error", err)
				return
			}
			rewritten[idx] = out.URL
		}(idx, abs)
	}
	wg.Wait()

	var b strings.Builder
	last := 0
	for idx, ref := range refs {
		if rewritten[idx] == "" {
			continue
		}
		b.WriteString(content[last:ref.destStart])
		b.WriteString(rewritten[idx])
		last = ref.destEnd
	}
	b.WriteString(content[last:])
	return b.String()
}

// resolveImageURL returns the absolute http(s) URL for an image destination, or
// false when the destination needs no download (data URI, already a local
// asset, anchor) or cannot be resolved to a remote image.
func resolveImageURL(dest string, base *url.URL, baseErr error) (string, bool) {
	trimmed := strings.TrimSpace(dest)
	if trimmed == "" {
		return "", false
	}
	if strings.HasPrefix(trimmed, "data:") || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	if strings.HasPrefix(trimmed, "/assets/") || strings.HasPrefix(trimmed, "assets/") {
		return "", false
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return "", false
	}
	if !u.IsAbs() {
		if baseErr != nil || base == nil {
			return "", false
		}
		u = base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	return u.String(), true
}

// byteFile adapts an in-memory payload to multipart.File.
type byteFile struct{ *bytes.Reader }

func (byteFile) Close() error { return nil }

// ─── Markdown image scanning ─────────────────────────────────────────────────

// imageRef is one Markdown image reference: the byte range of its destination
// inside the content plus the raw destination text.
type imageRef struct {
	destStart int
	destEnd   int
	dest      string
}

// markdownImageRefs finds Markdown image references outside fenced code blocks
// and inline code spans (within a line). Code examples must keep their remote
// references untouched.
func markdownImageRefs(content string) []imageRef {
	var refs []imageRef
	inFence := false
	var fenceChar byte
	fenceLen := 0

	for pos := 0; pos < len(content); {
		line := content[pos:]
		if nl := strings.IndexByte(line, '\n'); nl >= 0 {
			line = line[:nl]
		}
		lineStart := pos
		pos += len(line) + 1

		trimmed := strings.TrimLeft(line, " \t")
		indent := len(line) - len(trimmed)
		if !inFence && indent <= 3 {
			if run := leadingByteRun(trimmed, '`'); run >= 3 {
				inFence, fenceChar, fenceLen = true, '`', run
				continue
			}
			if run := leadingByteRun(trimmed, '~'); run >= 3 {
				inFence, fenceChar, fenceLen = true, '~', run
				continue
			}
		}
		if inFence && indent <= 3 && len(trimmed) >= fenceLen && leadingByteRun(trimmed, fenceChar) >= fenceLen {
			inFence = false
			continue
		}
		if inFence {
			continue
		}
		refs = append(refs, imageRefsInLine(line, lineStart)...)
	}
	return refs
}

// imageRefsInLine finds image references in a single line, skipping inline
// code spans. An unclosed backtick run makes the rest of the line literal.
func imageRefsInLine(line string, lineStart int) []imageRef {
	var refs []imageRef
	for i := 0; i < len(line); {
		if line[i] == '`' {
			run := leadingByteRun(line[i:], '`')
			closer := strings.Index(line[i+run:], strings.Repeat("`", run))
			if closer < 0 {
				break
			}
			i += run + closer + run
			continue
		}
		if line[i] == '!' && i+1 < len(line) && line[i+1] == '[' {
			if ref, ok := parseImageRef(line, i, lineStart); ok {
				refs = append(refs, ref)
				i = ref.destEnd - lineStart
				continue
			}
		}
		i++
	}
	return refs
}

// parseImageRef parses ![alt](dest) (with an optional angle-bracketed
// destination) starting at the '!' in line. References carrying a title are
// left untouched.
func parseImageRef(line string, start, lineStart int) (imageRef, bool) {
	altEnd := strings.IndexByte(line[start+2:], ']')
	if altEnd < 0 {
		return imageRef{}, false
	}
	altEnd += start + 2
	if altEnd+1 >= len(line) || line[altEnd+1] != '(' {
		return imageRef{}, false
	}

	destStart := altEnd + 2
	closeIdx := strings.IndexByte(line[destStart:], ')')
	if closeIdx < 0 {
		return imageRef{}, false
	}
	closeIdx += destStart

	dest := strings.TrimSpace(line[destStart:closeIdx])
	if strings.HasPrefix(dest, "<") {
		end := strings.IndexByte(dest, '>')
		if end < 0 {
			return imageRef{}, false
		}
		dest = dest[1:end]
	} else if strings.IndexAny(dest, " \t") >= 0 {
		// A title follows the destination; leave these references untouched.
		return imageRef{}, false
	}
	if dest == "" {
		return imageRef{}, false
	}

	return imageRef{destStart: lineStart + destStart, destEnd: lineStart + closeIdx, dest: dest}, true
}

func leadingByteRun(s string, b byte) int {
	n := 0
	for n < len(s) && s[n] == b {
		n++
	}
	return n
}
