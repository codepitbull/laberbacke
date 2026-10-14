// Package web is the HTTP side of the chat: the page, the SSE answer stream,
// document downloads and speech transcription.
//
// The page posts a prompt to /s/{session}/chat, which returns an HTML
// fragment that opens an SSE connection to /s/{session}/stream/{id}. The
// stream handler runs the model and pushes each token as an SSE event.
package web

import (
	"cmp"
	_ "embed"
	"html/template"
	"log"
	"net/http"
	"sync"

	"github.com/ardanlabs/kronk/sdk/bucky"
	"github.com/google/uuid"

	"kronkplay/internal/chat"
	"kronkplay/internal/session"
)

//go:embed index.html
var indexHTML string

var pageTmpl = template.Must(template.New("page").Parse(indexHTML))

// Config holds what the server needs.
type Config struct {
	Chat     *chat.Engine
	Speech   *bucky.Bucky
	Sessions *session.Store

	// ModelName is shown in the page header when the model reports no id.
	ModelName string
}

type Server struct {
	Config

	mu      sync.Mutex
	pending map[string]pendingPrompt // stream id -> prompt waiting for its stream
}

type pendingPrompt struct {
	session string
	prompt  string
}

func New(cfg Config) *Server {
	return &Server{Config: cfg, pending: map[string]pendingPrompt{}}
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/s/"+uuid.NewString(), http.StatusFound)
	})
	mux.HandleFunc("GET /s/{session}", s.page)
	mux.HandleFunc("POST /s/{session}/chat", s.chat)
	mux.HandleFunc("GET /s/{session}/stream/{id}", s.stream)
	mux.HandleFunc("POST /s/{session}/transcribe", s.transcribe)
	mux.HandleFunc("GET /s/{session}/docs/{name}", s.downloadDoc)
	return mux
}

// session resolves the {session} path value, answering 404 if it is invalid.
func (s *Server) session(w http.ResponseWriter, r *http.Request) *session.Session {
	sess, err := s.Sessions.Get(r.PathValue("session"))
	if err != nil {
		http.NotFound(w, r)
		return nil
	}
	return sess
}

// page renders the full chat page for a session.
func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	sess := s.session(w, r)
	if sess == nil {
		return
	}

	sess.Mu.Lock()
	data := map[string]any{
		"ID":       sess.ID,
		"Model":    cmp.Or(s.Chat.ModelID(), s.ModelName),
		"Title":    sess.Title,
		"Entries":  sess.Entries,
		"Sessions": template.HTML(s.sessionItems(sess.ID)),
		"Docs":     template.HTML(docItems(sess)),
	}
	sess.Mu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTmpl.Execute(w, data); err != nil {
		log.Print(err)
	}
}
