// Package web provides a simple web UI for norma.
package web

import (
	"embed"
	"html/template"
	"net/http"

	"github.com/metalagman/norma/internal/task"
)

// Server provides the web UI handlers and state.
type Server struct {
	tracker task.Tracker
}

// NewServer creates a new web server.
func NewServer(tracker task.Tracker) (*Server, error) {
	return &Server{tracker: tracker}, nil
}

//go:embed templates/*.html
var templatesFS embed.FS

// Routes returns the router for the web UI.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("POST /tasks/{id}/done", s.handleMarkDone)
	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFS(templatesFS, "templates/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items, err := s.tracker.List(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tmpl.Execute(w, items); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleMarkDone(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.tracker.MarkDone(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
