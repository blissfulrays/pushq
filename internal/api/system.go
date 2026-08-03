package api

import (
	"errors"
	"net/http"

	"github.com/blissfulrays/pushq/internal/api/types"
	"github.com/blissfulrays/pushq/internal/version"
)

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Ping(r.Context()); err != nil {
		writeProblem(w, r, http.StatusServiceUnavailable, CodeInternal, "database unreachable")
		return
	}
	role := "api"
	if s.Role != nil {
		role = s.Role()
	}
	writeJSON(w, http.StatusOK, types.ReadyResponse{Role: role, Dialect: s.Store.Dialect()})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, types.VersionResponse{
		Version: version.Version,
		Commit:  version.Commit,
		Dialect: s.Store.Dialect(),
	})
}

func isBadRequest(err error) bool {
	var br badRequest
	return errors.As(err, &br)
}

func isStoreErr(err, target error) bool {
	return errors.Is(err, target)
}
