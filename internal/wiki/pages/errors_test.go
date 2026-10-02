package pages

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/favorites"
)

// TestRespondWithPageError_FavoritesStoreUnavailable_Returns503 is the
// regression test for a real bug found in review: favorites use cases
// propagate favorites.ErrCodeFavoritesStoreUnavailable (surfaced while a
// live restore has favorites.db suspended) unwrapped, but pageErrorStatus
// had no case for it and fell through to the generic 500 default — unlike
// apikeys' equivalent case for its own store, which already maps to 503. A
// client can't tell "retry shortly" apart from "something is broken"
// without this.
func TestRespondWithPageError_FavoritesStoreUnavailable_Returns503(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	respondWithPageError(c, sharederrors.NewLocalizedError(
		favorites.ErrCodeFavoritesStoreUnavailable,
		"The server is restoring from a backup — please try again in a moment",
		"favorites store is suspended for an in-progress restore",
		nil,
	))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

// TestRespondWithPageError_URLImportMappings locks in the HTTP contract for the
// "import/generate from URL" endpoints: an unconfigured LLM and a failed fetch
// must both return a 400 with a dedicated, non-leaking error code rather than
// the generic 500 default (which would also expose raw network errors).
func TestRespondWithPageError_URLImportMappings(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	cases := []struct {
		name string
		err  error
		code string
	}{
		{"llm not configured", ErrLLMNotConfigured, ErrCodePageLLMNotConfigured},
		{"invalid url", ErrImportURLInvalid, ErrCodePageInvalidPayload},
		{"import failed", fmt.Errorf("%w: dial tcp: lookup internal.example", ErrImportFailed), ErrCodePageImportFailed},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			respondWithPageError(c, tc.err)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			var body PageErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Error.Code != tc.code {
				t.Errorf("code = %q, want %q", body.Error.Code, tc.code)
			}
		})
	}
}
