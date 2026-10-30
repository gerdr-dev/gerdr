package server

import (
	"net/http"

	"github.com/gerdr-dev/gerdr/internal/doctor"
)

// Settings → Labs → Diagnosis (internal/doctor). Open to remote devices too: the fixes here only re-read
// PATH or (from the UI, via config) pick which install to run, and a phone is often where the
// "npx not found" in an agent's reply gets noticed.
func (s *Server) doctorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/doctor", func(w http.ResponseWriter, r *http.Request) {
		if s.Doctor == nil {
			writeJSON(w, nil)
			return
		}
		rep := s.Doctor.Last()
		if rep == nil {
			v := s.Doctor.Run(r.Context(), false)
			rep = &v
		}
		writeJSON(w, rep)
	})
	mux.HandleFunc("POST /api/doctor/run", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Deep bool `json:"deep"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		if s.Doctor == nil {
			httpError(w, http.StatusNotFound, "diagnosis is off")
			return
		}
		writeJSON(w, s.Doctor.Run(r.Context(), b.Deep))
	})
	mux.HandleFunc("POST /api/doctor/fix", func(w http.ResponseWriter, r *http.Request) {
		var f doctor.Fix
		if err := decode(r, &f); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		if s.Doctor == nil {
			httpError(w, http.StatusNotFound, "diagnosis is off")
			return
		}
		rep, err := s.Doctor.Fix(r.Context(), f)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, rep)
	})
}
