package server

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/gin-gonic/gin"
)

type importRoutes struct {
	auth       *Authentication
	repository imports.Repository
}

func mountImports(router *gin.Engine, auth *Authentication, repository imports.Repository) {
	if auth == nil || repository == nil {
		return
	}
	routes := &importRoutes{auth, repository}
	router.POST("/api/v1/imports", auth.RequireSession(), routes.create)
	router.GET("/api/v1/imports/:id", auth.RequireSession(), routes.get)
	router.POST("/api/v1/imports/:id/commit", auth.RequireSession(), routes.commit)
}

func (r *importRoutes) fail(c *gin.Context, err error) {
	code := apicontract.InternalError
	switch {
	case errors.Is(err, imports.ErrInvalidInput), errors.Is(err, imports.ErrUnsupported):
		code = apicontract.ValidationFailed
	case errors.Is(err, imports.ErrNotFound):
		code = apicontract.ResourceNotFound
	case errors.Is(err, imports.ErrRevisionConflict):
		code = apicontract.RevisionMismatch
	case errors.Is(err, imports.ErrIdempotencyConflict):
		code = apicontract.IdempotencyConflict
	case errors.Is(err, imports.ErrStateConflict), errors.Is(err, imports.ErrExpired):
		code = apicontract.StateConflict
	case errors.Is(err, imports.ErrUnavailable):
		code = apicontract.ServiceUnavailable
	default:
		var boundary *apicontract.Error
		if errors.As(err, &boundary) {
			r.auth.fail(c, boundary)
			return
		}
	}
	r.auth.fail(c, apicontract.NewError(code))
}

func importRequestKey(c *gin.Context, route string) (string, error) {
	if len(c.Request.Header.Values("Idempotency-Key")) == 0 {
		return "", nil
	}
	session, _ := SessionFromContext(c.Request.Context())
	metadata, err := apicontract.ReadIdempotency(c.Request.Header, session.User.ScopeID, session.User.ID, route)
	return metadata.Key, err
}

func (r *importRoutes) create(c *gin.Context) {
	if len(c.Request.URL.Query()) != 0 {
		r.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return
	}
	key, err := importRequestKey(c, "import.create")
	if err != nil {
		r.fail(c, err)
		return
	}
	if len(c.Request.Header.Values("Content-Type")) != 1 {
		r.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return
	}
	media, params, err := mime.ParseMediaType(c.Request.Header.Get("Content-Type"))
	if err != nil {
		r.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return
	}
	var text []byte
	var format string
	switch media {
	case "application/json":
		var request struct {
			Text     string `json:"text"`
			Format   string `json:"format"`
			SourceID *ir.ID `json:"source_id,omitempty"`
		}
		if err := apicontract.ReadRequest(c.Request, "ImportCreateRequest", &request); err != nil {
			r.fail(c, err)
			return
		}
		if request.SourceID != nil {
			r.fail(c, imports.ErrUnsupported)
			return
		}
		text = []byte(request.Text)
		format = request.Format
	case "multipart/form-data":
		text, format, err = readImportMultipart(c, params["boundary"])
		if err != nil {
			r.fail(c, err)
			return
		}
	default:
		r.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return
	}
	defer clear(text)
	if len(text) > imports.MaxInputBytes {
		r.fail(c, apicontract.NewError(apicontract.InputLimitExceeded))
		return
	}
	if len(text) == 0 || !utf8.Valid(text) {
		r.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return
	}
	session, _ := SessionFromContext(c.Request.Context())
	accepted, err := r.repository.Create(c.Request.Context(), imports.CreateInput{ScopeID: session.User.ScopeID, PrincipalID: session.User.ID, Text: text, InputFormat: format, IdempotencyKey: key})
	if err != nil {
		r.fail(c, err)
		return
	}
	if accepted.Replayed {
		c.Header("Idempotency-Replayed", "true")
	}
	etag, _ := apicontract.ETag(int64(accepted.Revision))
	c.Header("ETag", etag)
	respond(c, http.StatusAccepted, gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": accepted})
}

// Files are streamed from a bounded reader and never placed on disk. The
// multipart filename is ignored and cannot select a server-side path.
func readImportMultipart(c *gin.Context, boundary string) ([]byte, string, error) {
	if boundary == "" || len(boundary) > 70 {
		return nil, "", apicontract.NewError(apicontract.MalformedRequest)
	}
	const maximum = imports.MaxInputBytes + (64 << 10)
	if c.Request.ContentLength > maximum {
		return nil, "", apicontract.NewError(apicontract.InputLimitExceeded)
	}
	reader := multipart.NewReader(http.MaxBytesReader(c.Writer, c.Request.Body, maximum), boundary)
	var file []byte
	var format string
	seen := map[string]bool{}
	failed := func(err error) ([]byte, string, error) { clear(file); return nil, "", err }
	for {
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return failed(apicontract.NewError(apicontract.InputLimitExceeded))
			}
			return failed(apicontract.NewError(apicontract.MalformedRequest))
		}
		name := part.FormName()
		if seen[name] || name != "file" && name != "format" && name != "source_id" {
			part.Close()
			return failed(apicontract.NewError(apicontract.MalformedRequest))
		}
		seen[name] = true
		if name == "source_id" {
			part.Close()
			return failed(imports.ErrUnsupported)
		}
		if part.Header.Get("Content-Transfer-Encoding") != "" {
			part.Close()
			return failed(apicontract.NewError(apicontract.MalformedRequest))
		}
		limit := imports.MaxInputBytes
		if name == "format" {
			limit = 128
		}
		data, err := io.ReadAll(io.LimitReader(part, int64(limit)+1))
		part.Close()
		if len(data) > limit {
			clear(data)
			return failed(apicontract.NewError(apicontract.InputLimitExceeded))
		}
		if err != nil {
			clear(data)
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return failed(apicontract.NewError(apicontract.InputLimitExceeded))
			}
			return failed(apicontract.NewError(apicontract.MalformedRequest))
		}
		if !utf8.Valid(data) {
			clear(data)
			return failed(apicontract.NewError(apicontract.MalformedRequest))
		}
		if name == "file" {
			file = data
		} else {
			format = string(data)
			clear(data)
		}
	}
	if !seen["file"] || !seen["format"] || len(file) == 0 {
		return failed(apicontract.NewError(apicontract.MalformedRequest))
	}
	return file, format, nil
}

func (r *importRoutes) get(c *gin.Context) {
	id := ir.ID(c.Param("id"))
	if id.Validate() != nil {
		r.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return
	}
	query := c.Request.URL.Query()
	for field := range query {
		if field != "cursor" && field != "limit" {
			r.fail(c, apicontract.NewError(apicontract.MalformedRequest))
			return
		}
	}
	page, err := apicontract.ParsePagination(query)
	if err != nil {
		r.fail(c, err)
		return
	}
	session, _ := SessionFromContext(c.Request.Context())
	b, err := r.repository.Get(c.Request.Context(), session.User.ScopeID, id, imports.PageOptions{Limit: page.Limit, Cursor: page.Cursor})
	if err != nil {
		r.fail(c, err)
		return
	}
	etag, _ := apicontract.ETag(int64(b.Revision))
	c.Header("ETag", etag)
	info := gin.H{"limit": page.Limit}
	if b.NextCursor != "" {
		info["next_cursor"] = b.NextCursor
	}
	respond(c, http.StatusOK, gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": b, "page": info})
}

func (r *importRoutes) commit(c *gin.Context) {
	if len(c.Request.URL.Query()) != 0 {
		r.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return
	}
	id := ir.ID(c.Param("id"))
	if id.Validate() != nil {
		r.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return
	}
	expected, err := apicontract.ParseIfMatch(c.Request.Header)
	if err != nil {
		r.fail(c, err)
		return
	}
	key, err := importRequestKey(c, "import.commit")
	if err != nil {
		r.fail(c, err)
		return
	}
	if len(c.Request.Header.Values("Content-Type")) != 1 {
		r.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return
	}
	var request struct {
		Decisions       []imports.Decision    `json:"decisions"`
		SourceRevision  *apicontract.Revision `json:"source_revision,omitempty"`
		BindingRevision *apicontract.Revision `json:"binding_revision,omitempty"`
	}
	if err := apicontract.ReadRequest(c.Request, "ImportCommitRequest", &request); err != nil {
		r.fail(c, err)
		return
	}
	if request.SourceRevision != nil || request.BindingRevision != nil {
		r.fail(c, imports.ErrUnsupported)
		return
	}
	for _, d := range request.Decisions {
		if d.Action == "bind" {
			r.fail(c, imports.ErrUnsupported)
			return
		}
	}
	session, _ := SessionFromContext(c.Request.Context())
	result, err := r.repository.Commit(c.Request.Context(), imports.CommitInput{ScopeID: session.User.ScopeID, PrincipalID: session.User.ID, BatchID: id, ExpectedRevision: expected, IdempotencyKey: key, RequestID: apicontract.RequestID(c.Request.Context()), Decisions: request.Decisions})
	if err != nil {
		r.fail(c, err)
		return
	}
	if result.Replayed {
		c.Header("Idempotency-Replayed", "true")
	}
	etag, _ := apicontract.ETag(int64(result.Revision))
	c.Header("ETag", etag)
	respond(c, http.StatusOK, gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": result})
}
