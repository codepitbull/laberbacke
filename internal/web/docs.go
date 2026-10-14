package web

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"kronkplay/internal/session"
)

func (s *Server) downloadDoc(w http.ResponseWriter, r *http.Request) {
	sess := s.session(w, r)
	if sess == nil {
		return
	}

	name := r.PathValue("name")
	if strings.HasPrefix(name, ".") {
		http.NotFound(w, r)
		return
	}

	root, err := os.OpenRoot(sess.Dir())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()

	f, err := root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// docItems renders the documents panel's list items for a session.
func docItems(sess *session.Session) string {
	var b strings.Builder
	for _, name := range sess.DocNames() {
		fmt.Fprintf(&b, `<li><a href="/s/%s/docs/%s" download>%s</a></li>`,
			sess.ID, html.EscapeString(url.PathEscape(name)), html.EscapeString(name))
	}
	return b.String()
}

// sessionItems renders the sessions panel's list items, marking the current one.
func (s *Server) sessionItems(current string) string {
	var b strings.Builder
	for _, sum := range s.Sessions.List() {
		class := ""
		if sum.ID == current {
			class = ` class="current"`
		}
		fmt.Fprintf(&b, `<li%s><a href="/s/%s" title="%s">%s</a></li>`,
			class, sum.ID, sum.Created.Format(time.DateTime), html.EscapeString(sum.Title))
	}
	return b.String()
}
