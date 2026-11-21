package whttp

import (
	"errors"
	"net/http"
)

// Error is a domain error that knows its HTTP status. Declare sentinels with the constructors
// below (var ErrNotFound = whttp.NotFound("app not found")); errors.Is still matches them and
// WriteError answers with their status, so no package needs its own error switch.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string   { return e.Msg }
func (e *Error) HTTPStatus() int { return e.Status }

func Invalid(msg string) error       { return &Error{http.StatusBadRequest, msg} }
func Unauthorized(msg string) error  { return &Error{http.StatusUnauthorized, msg} }
func Forbidden(msg string) error     { return &Error{http.StatusForbidden, msg} }
func NotFound(msg string) error      { return &Error{http.StatusNotFound, msg} }
func Conflict(msg string) error      { return &Error{http.StatusConflict, msg} }
func Unprocessable(msg string) error { return &Error{http.StatusUnprocessableEntity, msg} }
func Upstream(msg string) error      { return &Error{http.StatusBadGateway, msg} } // a node, GitHub or Cloudflare failed

// StatusOf is the HTTP status of the first error in err's chain that names one, else 500.
func StatusOf(err error) int {
	var s interface{ HTTPStatus() int }
	if errors.As(err, &s) {
		return s.HTTPStatus()
	}
	return http.StatusInternalServerError
}

// WriteError answers with err's message and StatusOf(err).
func WriteError(w http.ResponseWriter, err error) {
	ErrorJSON(w, StatusOf(err), err.Error())
}

// Reply writes v with code, or err if there is one: the end of most handlers.
func Reply(w http.ResponseWriter, code int, v any, err error) {
	if err != nil {
		WriteError(w, err)
		return
	}
	JSON(w, code, v)
}
