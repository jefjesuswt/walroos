package sshkeys

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/shared/whttp"
	"golang.org/x/crypto/ssh"
)

// NodeAuth checks a node's id and token (fleet.Service.Authenticate).
type NodeAuth func(ctx context.Context, id, token string) error

type Handler struct {
	store    *Store
	nodeAuth NodeAuth
}

func NewHandler(store *Store, nodeAuth NodeAuth) *Handler {
	return &Handler{store: store, nodeAuth: nodeAuth}
}

// UserRoutes must be mounted behind user auth: each user manages their own keys.
func (h *Handler) UserRoutes(r *whttp.Router) {
	r.Get("/api/ssh-keys", h.handleList)
	r.Post("/api/ssh-keys", h.handleAdd)
	r.Delete("/api/ssh-keys/{id}", h.handleDelete)
}

// NodeRoutes are called by node agents with their node token.
func (h *Handler) NodeRoutes(r *whttp.Router) {
	r.Post("/api/nodes/{id}/ssh-auth", h.handleAuthorize)
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	keys, err := h.store.List(r.Context(), u.Login)
	if err != nil {
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	whttp.JSON(w, http.StatusOK, keys)
}

func (h *Handler) handleAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	k, err := parseKey(body.Name, body.PublicKey)
	if err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	u, _ := auth.FromContext(r.Context())
	err = h.store.Add(r.Context(), u.Login, k)
	switch {
	case errors.Is(err, ErrKeyExists):
		whttp.ErrorJSON(w, http.StatusConflict, err.Error())
	case err != nil:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	default:
		whttp.JSON(w, http.StatusCreated, k)
	}
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	err := h.store.Delete(r.Context(), u.Login, r.PathValue("id"))
	switch {
	case errors.Is(err, ErrKeyNotFound):
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
	case err != nil:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if err := h.nodeAuth(r.Context(), r.PathValue("id"), token); token == "" || err != nil {
		whttp.ErrorJSON(w, http.StatusUnauthorized, "invalid node token")
		return
	}
	var body struct {
		Workspace   string `json:"workspace"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	login, err := h.store.Authorize(r.Context(), r.PathValue("id"), body.Workspace, body.Fingerprint)
	switch {
	case errors.Is(err, ErrDenied):
		whttp.ErrorJSON(w, http.StatusForbidden, err.Error())
	case err != nil:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	default:
		whttp.JSON(w, http.StatusOK, map[string]string{"login": login})
	}
}

// parseKey validates an authorized_keys line. The name defaults to the key's comment.
func parseKey(name, line string) (Key, error) {
	pub, comment, _, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || strings.TrimSpace(string(rest)) != "" {
		return Key{}, ErrInvalidKey
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = comment
	}
	if name == "" || len(name) > 64 {
		return Key{}, ErrInvalidName
	}
	b := make([]byte, 8)
	rand.Read(b)
	return Key{
		ID:          hex.EncodeToString(b),
		Name:        name,
		PublicKey:   strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))),
		Fingerprint: ssh.FingerprintSHA256(pub),
		CreatedAt:   time.Now().UTC(),
	}, nil
}
