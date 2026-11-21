package whttp

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

type upstream struct{}

func (upstream) Error() string   { return "node said no" }
func (upstream) HTTPStatus() int { return http.StatusBadGateway }

func TestWriteError(t *testing.T) {
	errMissing := NotFound("thing not found")
	for _, c := range []struct {
		err  error
		code int
	}{
		{errMissing, 404},
		{fmt.Errorf("load: %w", errMissing), 404},
		{Invalid("bad"), 400},
		{Conflict("taken"), 409},
		{upstream{}, 502},
		{errors.New("disk on fire"), 500},
	} {
		rec := httptest.NewRecorder()
		WriteError(rec, c.err)
		if rec.Code != c.code {
			t.Errorf("%v: %d, want %d", c.err, rec.Code, c.code)
		}
	}
	if !errors.Is(fmt.Errorf("x: %w", errMissing), errMissing) {
		t.Error("a wrapped sentinel no longer matches errors.Is")
	}
}
