package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/blissfulrays/pushq/internal/api/types"
	"github.com/blissfulrays/pushq/internal/store"
)

const maxBatchTasks = 500

func (s *Server) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	queue := chi.URLParam(r, "queue")
	var body types.TaskCreate
	// Body limit: task body is base64 (4/3 overhead) plus envelope.
	if err := s.decodeJSON(w, r, &body, s.Limits.MaxBodyBytes*2+(64<<10)); err != nil {
		s.writeErr(w, r, err)
		return
	}
	q, err := s.Store.GetQueue(r.Context(), queue)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	now := s.Now()
	nt, err := taskCreateToNewTask(queue, body, s.Limits, q.Target.BaseURL != "", now)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	t, err := s.Store.CreateTask(r.Context(), nt, now)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, taskToResponse(t))
}

func (s *Server) handleTaskBatch(w http.ResponseWriter, r *http.Request) {
	queue := chi.URLParam(r, "queue")
	var body types.TaskBatchCreate
	if err := s.decodeJSON(w, r, &body, 8<<20); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if len(body.Tasks) == 0 {
		s.writeErr(w, r, errf("tasks must not be empty"))
		return
	}
	if len(body.Tasks) > maxBatchTasks {
		s.writeErr(w, r, errf("batch exceeds %d tasks", maxBatchTasks))
		return
	}
	q, err := s.Store.GetQueue(r.Context(), queue)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	now := s.Now()

	results := make([]types.BatchResult, len(body.Tasks))
	var newTasks []store.NewTask
	var newIdx []int
	for i, dto := range body.Tasks {
		nt, err := taskCreateToNewTask(queue, dto, s.Limits, q.Target.BaseURL != "", now)
		if err != nil {
			results[i] = types.BatchResult{Index: i, Error: batchError(err)}
			continue
		}
		newTasks = append(newTasks, nt)
		newIdx = append(newIdx, i)
	}

	if len(newTasks) > 0 {
		created, err := s.Store.CreateTasks(r.Context(), newTasks, now)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		for j, res := range created {
			i := newIdx[j]
			if res.Err != nil {
				results[i] = types.BatchResult{Index: i, Error: batchError(res.Err)}
			} else {
				t := taskToResponse(res.Task)
				results[i] = types.BatchResult{Index: i, Task: &t}
			}
		}
	}
	writeJSON(w, http.StatusOK, types.BatchResponse{Results: results})
}

func batchError(err error) *types.BatchError {
	code := CodeInternal
	switch {
	case isBadRequest(err):
		code = CodeInvalidArgument
	case isStoreErr(err, store.ErrAlreadyExists):
		code = CodeAlreadyExists
	case isStoreErr(err, store.ErrNotFound):
		code = CodeNotFound
	case isStoreErr(err, store.ErrFailedPrecondition):
		code = CodeFailedPrecondition
	}
	return &types.BatchError{Code: code, Detail: err.Error()}
}

func (s *Server) handleTaskGet(w http.ResponseWriter, r *http.Request) {
	view := store.ViewBasic
	if r.URL.Query().Get("view") == "full" {
		view = store.ViewFull
	}
	t, err := s.Store.GetTask(r.Context(), chi.URLParam(r, "queue"), chi.URLParam(r, "taskID"), view)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskToResponse(t))
}

func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	qp := r.URL.Query()
	opts := store.ListTasksOpts{
		Queue:     chi.URLParam(r, "queue"),
		IDPrefix:  qp.Get("id_prefix"),
		PageToken: qp.Get("page_token"),
		View:      store.ViewBasic,
	}
	if qp.Get("view") == "full" {
		opts.View = store.ViewFull
	}
	switch st := qp.Get("state"); st {
	case "":
	case "pending", "succeeded", "failed":
		opts.State = store.TaskState(st)
	case "running":
		opts.State = store.TaskLeased
	default:
		s.writeErr(w, r, errf("state must be pending, running, succeeded, or failed"))
		return
	}
	if v := qp.Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			s.writeErr(w, r, errf("page_size must be an integer"))
			return
		}
		opts.PageSize = n
	}
	tasks, next, err := s.Store.ListTasks(r.Context(), opts)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := types.TaskList{Tasks: []types.TaskResponse{}, NextPageToken: next}
	for _, t := range tasks {
		out.Tasks = append(out.Tasks, taskToResponse(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	err := s.Store.DeleteTask(r.Context(), chi.URLParam(r, "queue"), chi.URLParam(r, "taskID"), s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTaskRun(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.ForceRun(r.Context(), chi.URLParam(r, "queue"), chi.URLParam(r, "taskID"), s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskToResponse(t))
}

func (s *Server) handleAttemptsList(w http.ResponseWriter, r *http.Request) {
	attempts, err := s.Store.ListAttempts(r.Context(), chi.URLParam(r, "queue"), chi.URLParam(r, "taskID"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := types.AttemptList{Attempts: []types.AttemptResponse{}}
	for _, a := range attempts {
		out.Attempts = append(out.Attempts, types.AttemptResponse{
			Number:         a.Number,
			DispatchTime:   a.DispatchTime,
			ResponseTime:   a.ResponseTime,
			ResponseStatus: a.ResponseStatus,
			ErrorKind:      a.ErrorKind,
			ErrorSnippet:   a.ErrorSnippet,
			DurationMS:     a.Duration.Milliseconds(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
