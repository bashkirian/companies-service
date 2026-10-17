package http

import (
	"errors"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bashkirian/companies-service/internal/domain"
)

const maxBodyBytes = 64 << 10

type companyHandler struct {
	svc CompanyService
	log *zap.Logger
}

func (h *companyHandler) create(w http.ResponseWriter, r *http.Request) {
	if !requireContentType(w, r, "application/json") {
		return
	}
	req, err := decodeCreate(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	params, err := req.toParams()
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	c, err := h.svc.Create(r.Context(), params)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/companies/"+c.ID.String())
	setETag(w, c.Version)
	writeJSON(w, http.StatusCreated, toResponse(c))
}

func (h *companyHandler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	c, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	setETag(w, c.Version)
	writeJSON(w, http.StatusOK, toResponse(c))
}

func (h *companyHandler) patch(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if !requireContentType(w, r, "application/merge-patch+json", "application/json") {
		return
	}
	version, ok := parseIfMatch(w, r)
	if !ok {
		return
	}
	params, err := decodePatch(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	c, err := h.svc.Patch(r.Context(), id, version, params)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	setETag(w, c.Version)
	writeJSON(w, http.StatusOK, toResponse(c))
}

func (h *companyHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	version, ok := parseIfMatch(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), id, version); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *companyHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		verr    *domain.ValidationError
		badBody badBodyError
	)
	switch {
	case errors.As(err, &verr):
		writeProblem(w, r, http.StatusBadRequest, "validation failed", verr.Fields)
	case errors.Is(err, errBodyTooLarge):
		writeProblem(w, r, http.StatusRequestEntityTooLarge, "body must be at most 64 KiB", nil)
	case errors.As(err, &badBody):
		writeProblem(w, r, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, domain.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, "company not found", nil)
	case errors.Is(err, domain.ErrNameTaken):
		writeProblem(w, r, http.StatusConflict, "company name already taken", nil)
	case errors.Is(err, domain.ErrVersionMismatch):
		writeProblem(w, r, http.StatusPreconditionFailed, "company was modified, fetch it again and retry", nil)
	default:
		h.log.Error("request failed", zap.String("request_id", middleware.GetReqID(r.Context())), zap.Error(err))
		writeProblem(w, r, http.StatusInternalServerError, "internal error", nil)
	}
}

func requireContentType(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if slices.Contains(allowed, mt) {
		return true
	}
	writeProblem(w, r, http.StatusUnsupportedMediaType, "Content-Type must be "+strings.Join(allowed, " or "), nil)
	return false
}

func parseID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "id must be a UUID", nil)
		return uuid.Nil, false
	}
	return id, true
}

func setETag(w http.ResponseWriter, version int64) {
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(version, 10)))
}

// parseIfMatch reads the version from a strong ETag like "3". The header is mandatory for writes.
func parseIfMatch(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		writeProblem(w, r, http.StatusPreconditionRequired, "If-Match header is required: send the ETag from GET /api/v1/companies/{id}", nil)
		return 0, false
	}
	unquoted, err := strconv.Unquote(raw)
	if err == nil {
		var v int64
		if v, err = strconv.ParseInt(unquoted, 10, 64); err == nil && v > 0 {
			return v, true
		}
	}
	writeProblem(w, r, http.StatusPreconditionFailed, "If-Match does not match the current company ETag", nil)
	return 0, false
}
