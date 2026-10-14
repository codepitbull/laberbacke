package web

import (
	"context"
	"crypto/rand"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ardanlabs/kronk/sdk/kronk/model"

	"kronkplay/internal/chat"
	"kronkplay/internal/session"
	"kronkplay/internal/tools"
)

// chat parks the prompt for the stream and returns the HTML for the user
// bubble plus an assistant bubble that connects to the SSE stream.
func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	sess := s.session(w, r)
	if sess == nil {
		return
	}

	prompt := strings.TrimSpace(r.FormValue("prompt"))
	if prompt == "" {
		http.Error(w, "empty prompt", http.StatusBadRequest)
		return
	}

	streamID := rand.Text()
	s.mu.Lock()
	s.pending[streamID] = pendingPrompt{session: sess.ID, prompt: prompt}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="msg user">%s</div>
<div class="msg bot" hx-ext="sse" sse-connect="/s/%s/stream/%s" sse-swap="token" sse-close="done" hx-swap="beforeend"></div>`,
		html.EscapeString(prompt), sess.ID, streamID)
}

// stream answers a pending prompt as SSE: each piece of output is a "token"
// event, followed by a final "done" event. Turns in one session are
// serialized by the session lock.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	sess := s.session(w, r)
	if sess == nil {
		return
	}

	s.mu.Lock()
	p, ok := s.pending[r.PathValue("id")]
	delete(s.pending, r.PathValue("id"))
	s.mu.Unlock()

	if !ok || p.session != sess.ID {
		http.NotFound(w, r)
		return
	}

	sess.Mu.Lock()
	defer sess.Mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	rc := http.NewResponseController(w)

	send := func(event, data string) {
		fmt.Fprintf(w, "event: %s\n", event)
		for line := range strings.SplitSeq(data, "\n") {
			fmt.Fprintf(w, "data: %s\n", line)
		}
		fmt.Fprint(w, "\n")
		rc.Flush()
	}

	if len(sess.Messages) == 0 {
		sess.Title = shorten(p.prompt, 40)
		sess.Created = time.Now()
		sess.Messages = s.Chat.Start()
	}
	sess.Messages = append(sess.Messages, model.TextMessage(model.RoleUser, p.prompt))
	sess.Entries = append(sess.Entries, session.Entry{Role: model.RoleUser, Blocks: []session.Block{{Text: p.prompt}}})

	// reply mirrors the bot bubble: notes and text in the order they appear.
	reply := session.Entry{Role: model.RoleAssistant}
	out := chat.Output{
		Token: func(tok string) {
			if n := len(reply.Blocks); n > 0 && !reply.Blocks[n-1].Note {
				reply.Blocks[n-1].Text += tok
			} else {
				reply.Blocks = append(reply.Blocks, session.Block{Text: tok})
			}
			send("token", html.EscapeString(tok))
		},
		Note: func(note string) {
			reply.Blocks = append(reply.Blocks, session.Block{Note: true, Text: note})
			send("token", `<div class="note">`+html.EscapeString(note)+`</div>`)
		},
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	sess.Messages = s.Chat.Reply(ctx, tools.Env{Dir: sess.Dir()}, sess.Messages, out)
	sess.Entries = append(sess.Entries, reply)

	if err := sess.Save(); err != nil {
		log.Printf("save session %s: %v", sess.ID, err)
	}

	// Refresh both side panels out of band: new documents show up on the
	// right, a first message adds the session on the left.
	send("token", fmt.Sprintf(`<ul hx-swap-oob="innerHTML:#docs">%s</ul><ul hx-swap-oob="innerHTML:#sessions">%s</ul>`,
		docItems(sess), s.sessionItems(sess.ID)))
	send("done", "")
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
