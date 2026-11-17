package api

import (
	"net/http"

	helpdocs "github.com/Herculade0/feldcore/docs/help"
)

func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request) {
	cat, err := helpdocs.Load()
	if err != nil {
		if s.log != nil {
			s.log.Error("help catalog", "err", err)
		}
		writeError(w, http.StatusInternalServerError, "Help is unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, cat)
}
