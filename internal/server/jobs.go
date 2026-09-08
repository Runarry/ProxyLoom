package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/gin-gonic/gin"
)

type jobHandler struct {
	auth    *Authentication
	store   jobs.ManagementRepository
	cursor  *apicontract.CursorCodec
	mu      sync.Mutex
	streams map[ir.ID]int
}

func mountJobs(router *gin.Engine, auth *Authentication, repository jobs.ManagementRepository, cursor *apicontract.CursorCodec) error {
	if router == nil || auth == nil || repository == nil || cursor == nil {
		return errors.New("job_dependencies_invalid")
	}
	h := &jobHandler{auth: auth, store: repository, cursor: cursor, streams: map[ir.ID]int{}}
	routes := router.Group("/api/v1/jobs", auth.RequireSession())
	routes.GET("", h.list)
	routes.GET("/:id", h.get)
	routes.POST("/:id/cancel", h.cancel)
	routes.GET("/:id/events", h.events)
	return nil
}
func (h *jobHandler) fail(c *gin.Context, err error) {
	code := apicontract.ServiceUnavailable
	switch {
	case errors.Is(err, jobs.ErrInvalidInput):
		code = apicontract.MalformedRequest
	case errors.Is(err, jobs.ErrNotFound):
		code = apicontract.ResourceNotFound
	case errors.Is(err, jobs.ErrConflict), errors.Is(err, jobs.ErrCanceled):
		code = apicontract.StateConflict
	case errors.Is(err, jobs.ErrRevisionConflict):
		code = apicontract.RevisionMismatch
	case errors.Is(err, jobs.ErrLeaseLost):
		code = apicontract.LeaseLost
	default:
		var boundary *apicontract.Error
		if errors.As(err, &boundary) {
			h.auth.fail(c, err)
			return
		}
	}
	h.auth.fail(c, apicontract.NewError(code))
}
func (h *jobHandler) id(c *gin.Context) (ir.ID, bool) {
	id := ir.ID(c.Param("id"))
	if id.Validate() != nil {
		h.fail(c, jobs.ErrInvalidInput)
		return "", false
	}
	return id, true
}
func jobScope(c *gin.Context) ir.ID {
	session, _ := SessionFromContext(c.Request.Context())
	return session.User.ScopeID
}
func jobSafeError(input *runnerprotocol.SafeError) *apicontract.ErrorBody {
	if input == nil {
		return nil
	}
	safe := runnerprotocol.Safe(input.Code)
	return &apicontract.ErrorBody{Code: apicontract.Code(safe.Code), Message: safe.Message, Details: []apicontract.Detail{}}
}
func jobRead(input jobs.Job) apicontract.Job {
	return apicontract.Job{JobID: input.ID, Revision: apicontract.Revision(input.Revision), Executor: apicontract.Executor(input.Executor), Type: apicontract.JobType(input.Type), State: apicontract.JobState(input.State), Attempt: input.Attempt, LeaseSeq: apicontract.Counter(input.LeaseSeq), CancelRequested: input.CancelRequested, CreatedAt: input.CreatedAt, BatchID: input.BatchID, CoreBuildID: input.CoreBuildID, Verdict: apicontract.Verdict(input.Verdict), StartedAt: input.StartedAt, FinishedAt: input.FinishedAt, Error: jobSafeError(input.Error)}
}
func batchRead(input jobs.Batch) apicontract.TestBatch {
	batch := apicontract.TestBatch{BatchID: input.ID, Revision: apicontract.Revision(input.Revision), State: apicontract.JobState(input.State), Verdict: apicontract.Verdict(input.Verdict), JobIDs: []ir.ID{}, Children: []apicontract.Job{}, Completed: input.Completed, Total: int32(len(input.Children)), CancelRequested: input.CancelRequested, EffectiveLimits: apicontract.TestLimits{DurationMS: input.EffectiveLimits.DurationMS, MaxBytes: input.EffectiveLimits.MaxBytes}, CreatedAt: input.CreatedAt}
	for _, job := range input.Children {
		batch.JobIDs = append(batch.JobIDs, job.ID)
		batch.Children = append(batch.Children, jobRead(job))
	}
	return batch
}
func (h *jobHandler) snapshot(c *gin.Context, status int, snapshot jobs.Snapshot) {
	var data any
	var revision int64
	var schema string
	if snapshot.Job != nil {
		data = apicontract.JobResponse{RequestID: apicontract.RequestID(c.Request.Context()), Data: jobRead(*snapshot.Job)}
		revision = snapshot.Job.Revision
		schema = "JobResponse"
	} else if snapshot.Batch != nil {
		data = apicontract.TestBatchResponse{RequestID: apicontract.RequestID(c.Request.Context()), Data: batchRead(*snapshot.Batch)}
		revision = snapshot.Batch.Revision
		schema = "TestBatchResponse"
	} else {
		h.fail(c, jobs.ErrUnavailable)
		return
	}
	if apicontract.ValidateDTO(schema, data) != nil {
		h.fail(c, jobs.ErrUnavailable)
		return
	}
	tag, err := apicontract.ETag(revision)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.Header("ETag", tag)
	respond(c, status, data)
}
func (h *jobHandler) get(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	if c.Request.URL.RawQuery != "" {
		h.fail(c, jobs.ErrInvalidInput)
		return
	}
	snapshot, err := h.store.Snapshot(c.Request.Context(), jobScope(c), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.snapshot(c, http.StatusOK, snapshot)
}
func (h *jobHandler) cancel(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	if c.Request.URL.RawQuery != "" {
		h.fail(c, jobs.ErrInvalidInput)
		return
	}
	expected, err := apicontract.ParseIfMatch(c.Request.Header)
	if err != nil {
		h.fail(c, err)
		return
	}
	var reason struct {
		Reason string `json:"reason"`
	}
	if !h.auth.readRequest(c, "ReasonRequest", &reason, false) {
		return
	}
	snapshot, err := h.store.Cancel(c.Request.Context(), jobScope(c), id, expected)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.snapshot(c, http.StatusAccepted, snapshot)
}
func (h *jobHandler) list(c *gin.Context) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		h.fail(c, jobs.ErrInvalidInput)
		return
	}
	filters := url.Values{}
	for key, values := range query {
		if len(values) != 1 {
			h.fail(c, jobs.ErrInvalidInput)
			return
		}
		switch key {
		case "cursor", "limit":
		case "state", "type", "executor", "batch_id":
			if values[0] == "" {
				h.fail(c, jobs.ErrInvalidInput)
				return
			}
			filters[key] = values
		default:
			h.fail(c, jobs.ErrInvalidInput)
			return
		}
	}
	pagination, err := apicontract.ParsePagination(query)
	if err != nil {
		h.fail(c, err)
		return
	}
	input := jobs.ListInput{ScopeID: jobScope(c), Limit: pagination.Limit, State: jobs.State(filters.Get("state")), Type: jobs.Type(filters.Get("type")), Executor: jobs.Executor(filters.Get("executor")), BatchID: ir.ID(filters.Get("batch_id"))}
	hash, err := apicontract.FilterHash(filters)
	if err != nil {
		h.fail(c, err)
		return
	}
	binding := apicontract.CursorBinding{ScopeID: input.ScopeID, Collection: "jobs", Sort: apicontract.CreatedAtIDSort, FilterHash: hash}
	if pagination.Cursor != "" {
		position, err := h.cursor.Decode(pagination.Cursor, binding)
		if err != nil {
			h.fail(c, err)
			return
		}
		input.AfterID = position.ID
		input.AfterCreatedAt = position.CreatedAt
	}
	page, err := h.store.List(c.Request.Context(), input)
	if err != nil {
		h.fail(c, err)
		return
	}
	response := struct {
		RequestID string               `json:"request_id"`
		Data      []apicontract.Job    `json:"data"`
		Page      apicontract.PageInfo `json:"page"`
	}{RequestID: apicontract.RequestID(c.Request.Context()), Data: []apicontract.Job{}, Page: apicontract.PageInfo{Limit: input.Limit}}
	for _, job := range page.Jobs {
		response.Data = append(response.Data, jobRead(job))
	}
	if page.HasMore && len(page.Jobs) > 0 {
		last := page.Jobs[len(page.Jobs)-1]
		response.Page.NextCursor, err = h.cursor.Encode(binding, apicontract.CursorPosition{ID: last.ID, CreatedAt: last.CreatedAt})
		if err != nil {
			h.fail(c, err)
			return
		}
	}
	if apicontract.ValidateDTO("JobListResponse", response) != nil {
		h.fail(c, jobs.ErrUnavailable)
		return
	}
	respond(c, http.StatusOK, response)
}
func (h *jobHandler) events(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	if c.Request.URL.RawQuery != "" {
		h.fail(c, jobs.ErrInvalidInput)
		return
	}
	after, _, err := apicontract.ParseLastEventID(c.Request.Header)
	if err != nil {
		h.fail(c, err)
		return
	}
	session, _ := SessionFromContext(c.Request.Context())
	h.mu.Lock()
	active := h.streams[session.User.ID]
	if active < 4 {
		h.streams[session.User.ID] = active + 1
	}
	h.mu.Unlock()
	if active >= 4 {
		h.fail(c, apicontract.NewError(apicontract.RateLimited))
		return
	}
	defer func() {
		h.mu.Lock()
		h.streams[session.User.ID]--
		if h.streams[session.User.ID] == 0 {
			delete(h.streams, session.User.ID)
		}
		h.mu.Unlock()
	}()
	page, err := h.store.Events(c.Request.Context(), session.User.ScopeID, id, int64(after), 200)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Accel-Buffering", "no")
	controller := http.NewResponseController(c.Writer)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lifetime := time.NewTimer(25 * time.Second)
	defer lifetime.Stop()
	for {
		// Bounded writes keep a slow client from retaining a worker indefinitely.
		if err = controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return
		}
		if page.Reset {
			_ = apicontract.WriteSnapshotReset(c.Writer, id, apicontract.Counter(page.LatestSeq))
			c.Writer.Flush()
			return
		}
		for _, event := range page.Events {
			wire := apicontract.JobEvent{JobID: event.JobID, Seq: apicontract.Revision(event.Seq), Phase: event.Phase, Completed: event.Completed, Total: event.Total, Verdict: apicontract.Verdict(event.Verdict), Error: jobSafeError(event.Error)}
			if apicontract.ValidateDTO("JobEvent", wire) != nil {
				return
			}
			data, marshalErr := json.Marshal(wire)
			if marshalErr != nil {
				return
			}
			if _, err = fmt.Fprintf(c.Writer, "id: %s\nevent: job_event\ndata: %s\n\n", strconv.FormatInt(event.Seq, 10), data); err != nil {
				return
			}
			after = apicontract.Counter(event.Seq)
		}
		if _, err = fmt.Fprint(c.Writer, ": heartbeat\n\n"); err != nil {
			return
		}
		c.Writer.Flush()
		if page.Terminal && int64(after) >= page.LatestSeq {
			return
		}
		if int64(after) < page.LatestSeq {
			page, err = h.store.Events(c.Request.Context(), session.User.ScopeID, id, int64(after), 200)
			if err != nil {
				return
			}
			continue
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-lifetime.C:
			return
		case <-ticker.C:
		}
		// Revocation and database failure close an established stream, too.
		current, authErr := h.auth.service.Authenticate(c.Request.Context(), session.ID)
		if authErr != nil || current.User.ScopeID != session.User.ScopeID || current.User.ID != session.User.ID || current.User.Role != "administrator" {
			return
		}
		page, err = h.store.Events(c.Request.Context(), session.User.ScopeID, id, int64(after), 200)
		if err != nil {
			return
		}
	}
}
