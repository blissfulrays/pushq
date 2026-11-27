package api

import (
	"net/http"
	"strconv"

	"github.com/blissfulrays/pushq/internal/api/types"
	"github.com/blissfulrays/pushq/internal/store"
)

func (s *Server) handleQueueCreate(w http.ResponseWriter, r *http.Request) {
	var body types.QueueCreate
	if err := s.decodeJSON(w, r, &body, 256<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u, err := queuePutToUpsert(body.Name, body.QueuePut)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	mapUpsert(r, &u)
	u.KeepSecret = false
	q, err := s.Store.CreateQueue(r.Context(), u, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, queueOut(r, q))
}

func (s *Server) handleQueuePut(w http.ResponseWriter, r *http.Request) {
	name := queueParam(r) // validated as sent; mapUpsert namespaces it below
	var body types.QueuePut
	if err := s.decodeJSON(w, r, &body, 256<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	u, err := queuePutToUpsert(name, body)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	mapUpsert(r, &u)
	q, outcome, err := s.Store.UpsertQueue(r.Context(), u, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	status := http.StatusOK
	result := "updated"
	switch outcome {
	case store.UpsertCreated:
		status = http.StatusCreated
		result = "created"
	case store.UpsertNoop:
		result = "unchanged"
	}
	w.Header().Set("X-Pushq-Result", result)
	writeJSON(w, status, queueOut(r, q))
}

func (s *Server) handleQueueGet(w http.ResponseWriter, r *http.Request) {
	q, err := s.Store.GetQueue(r.Context(), inName(r, queueParam(r)))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, queueOut(r, q))
}

func (s *Server) handleQueuePatch(w http.ResponseWriter, r *http.Request) {
	name := inName(r, queueParam(r))
	var body types.QueuePatch
	if err := s.decodeJSON(w, r, &body, 256<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	p, err := queuePatchToStore(body)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	// Validate the merged result inside the store transaction, before it is
	// written, so a patch can never persist invalid config.
	p.Validate = validateStored(r)
	if p.DeadLetterQueue != nil && *p.DeadLetterQueue != "" {
		dlq := inName(r, *p.DeadLetterQueue)
		p.DeadLetterQueue = &dlq
	}
	q, err := s.Store.PatchQueue(r.Context(), name, p, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, queueOut(r, q))
}

func (s *Server) handleQueueList(w http.ResponseWriter, r *http.Request) {
	opts := store.ListQueuesOpts{
		// A team only ever lists inside its own namespace.
		NamePrefix: teamPrefix(r) + r.URL.Query().Get("name_prefix"),
		PageToken:  r.URL.Query().Get("page_token"),
	}
	if v := r.URL.Query().Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			s.writeErr(w, r, errf("page_size must be an integer"))
			return
		}
		opts.PageSize = n
	}
	queues, next, err := s.Store.ListQueues(r.Context(), opts)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := types.QueueList{Queues: []types.QueueResponse{}, NextPageToken: next}
	for _, q := range queues {
		out.Queues = append(out.Queues, queueOut(r, q))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleQueueDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteQueue(r.Context(), inName(r, queueParam(r))); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleQueuePause(w http.ResponseWriter, r *http.Request) {
	q, err := s.Store.SetQueueState(r.Context(), inName(r, queueParam(r)), store.QueuePaused, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, queueOut(r, q))
}

func (s *Server) handleQueueResume(w http.ResponseWriter, r *http.Request) {
	q, err := s.Store.SetQueueState(r.Context(), inName(r, queueParam(r)), store.QueueRunning, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, queueOut(r, q))
}

func (s *Server) handleQueuePurge(w http.ResponseWriter, r *http.Request) {
	q, err := s.Store.PurgeQueue(r.Context(), inName(r, queueParam(r)), s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, queueOut(r, q))
}

func (s *Server) handleQueueStats(w http.ResponseWriter, r *http.Request) {
	name := inName(r, queueParam(r))
	now := s.Now()
	q, err := s.Store.GetQueue(r.Context(), name)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	st, err := s.Store.QueueStats(r.Context(), name, r.URL.Query().Get("id_prefix"), now)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := types.QueueStatsResponse{
		State:           string(q.State),
		Pending:         st.Pending,
		Running:         st.Running,
		Succeeded:       st.Succeeded,
		Failed:          st.Failed,
		ScheduledFuture: st.ScheduledFuture,
		OldestDueAt:     st.OldestDueAt,
		Dispatched1m:    st.Dispatched1m,
		Errors1m:        st.Errors1m,
		Dispatched1h:    st.Dispatched1h,
		Errors1h:        st.Errors1h,
		LastPurge:       q.PurgedAt,
	}
	if st.OldestDueAt != nil {
		age := now.Sub(*st.OldestDueAt).Seconds()
		if age < 0 {
			age = 0
		}
		out.OldestDueAge = &age
	}
	writeJSON(w, http.StatusOK, out)
}
