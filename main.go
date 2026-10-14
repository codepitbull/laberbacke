// A minimal web chat on top of Kronk. The page posts a prompt to
// /s/{session}/chat, which returns an HTML fragment that opens an SSE
// connection to /s/{session}/stream/{id}. The stream handler runs the model
// and pushes each token as an SSE event.
//
// Every chat is a session with a UUID, reachable at /s/{uuid}. Its directory
// sessions/{uuid}/ holds the documents the model created via the
// create_document tool, plus a chat.md transcript.
package main

import (
	"cmp"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ardanlabs/kronk/sdk/bucky"
	"github.com/ardanlabs/kronk/sdk/kronk"
	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/ardanlabs/kronk/sdk/tools/libs"
	"github.com/ardanlabs/kronk/sdk/tools/models"
	"github.com/google/uuid"
)

const maxToolTurns = 3

const systemPrompt = `You are a helpful assistant. Answer briefly and clearly.
When the user asks you to create, write or save a document or file, call the
create_document tool with a short filename and the full document content.
Otherwise just answer in the chat.`

//go:embed index.html
var indexHTML string

var pageTmpl = template.Must(template.New("page").Parse(indexHTML))

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	addr := flag.String("addr", "localhost:8080", "listen address")
	dataDir := flag.String("data", "sessions", "directory holding one folder per session")
	configPath := flag.String("config", "config.yml", "config file; defaults apply if it does not exist")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	log.Printf("model: %s, speech model: %s", cfg.Model, cfg.SpeechModel)

	store, err := newSessionStore(*dataDir)
	if err != nil {
		return err
	}

	krn, err := loadModel(cfg.Model)
	if err != nil {
		return err
	}
	defer krn.Unload(context.Background())

	speech, err := loadSpeech(cfg.SpeechModel)
	if err != nil {
		return err
	}
	defer speech.Unload(context.Background())

	app := &app{krn: krn, speech: speech, model: cfg.Model, store: store, pending: map[string]pendingPrompt{}}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/s/"+uuid.NewString(), http.StatusFound)
	})
	mux.HandleFunc("GET /s/{session}", app.page)
	mux.HandleFunc("POST /s/{session}/chat", app.chat)
	mux.HandleFunc("GET /s/{session}/stream/{id}", app.stream)
	mux.HandleFunc("POST /s/{session}/transcribe", app.transcribe)
	mux.HandleFunc("GET /s/{session}/docs/{name}", app.downloadDoc)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	abs, _ := filepath.Abs(*dataDir)
	log.Printf("listening on http://%s (sessions in %s)", *addr, abs)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// loadModel downloads llama.cpp and the model on first run, then loads it.
func loadModel(modelSource string) (*kronk.Kronk, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	lb, err := libs.New(libs.WithDetect(ctx, kronk.FmtLogger), libs.WithValidation(true))
	if err != nil {
		return nil, err
	}
	if _, err := lb.Download(ctx, kronk.FmtLogger); err != nil {
		return nil, fmt.Errorf("install llama.cpp: %w", err)
	}
	if err := kronk.Init(kronk.WithLibPath(lb.LibsPath())); err != nil {
		return nil, fmt.Errorf("init kronk: %w", err)
	}

	mdls, err := models.New()
	if err != nil {
		return nil, err
	}
	mp, err := mdls.Download(ctx, kronk.FmtLogger, modelSource)
	if err != nil {
		return nil, fmt.Errorf("install model: %w", err)
	}

	return kronk.New(model.WithModelFiles(mp.ModelFiles), model.WithAutoTune(true))
}

// tools describes the functions the model may call, in the OpenAI format.
var tools = model.DocumentArray(
	model.D{
		"type": "function",
		"function": model.D{
			"name":        "create_document",
			"description": "Create a text document that the user can download.",
			"parameters": model.D{
				"type": "object",
				"properties": model.D{
					"filename": model.D{
						"type":        "string",
						"description": "File name including extension, e.g. shopping-list.md",
					},
					"content": model.D{
						"type":        "string",
						"description": "The full text of the document.",
					},
				},
				"required": []any{"filename", "content"},
			},
		},
	},
)

type pendingPrompt struct {
	session string
	prompt  string
}

type app struct {
	krn    *kronk.Kronk
	speech *bucky.Bucky
	model  string // configured model source
	store *sessionStore

	mu      sync.Mutex
	pending map[string]pendingPrompt // stream id -> prompt waiting for its stream
}

// session resolves the {session} path value, answering 404 if it is invalid.
func (a *app) session(w http.ResponseWriter, r *http.Request) *Session {
	sess, err := a.store.get(r.PathValue("session"))
	if err != nil {
		http.NotFound(w, r)
		return nil
	}
	return sess
}

// page renders the full chat page for a session.
func (a *app) page(w http.ResponseWriter, r *http.Request) {
	sess := a.session(w, r)
	if sess == nil {
		return
	}

	sess.mu.Lock()
	data := map[string]any{
		"ID":       sess.ID,
		"Model":    cmp.Or(a.krn.ModelID(), a.model),
		"Title":    sess.Title,
		"Entries":  sess.Entries,
		"Sessions": template.HTML(a.sessionItems(sess.ID)),
		"Docs":     template.HTML(docItems(sess)),
	}
	sess.mu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTmpl.Execute(w, data); err != nil {
		log.Print(err)
	}
}

// chat parks the prompt for the stream and returns the HTML for the user
// bubble plus an assistant bubble that connects to the SSE stream.
func (a *app) chat(w http.ResponseWriter, r *http.Request) {
	sess := a.session(w, r)
	if sess == nil {
		return
	}

	prompt := strings.TrimSpace(r.FormValue("prompt"))
	if prompt == "" {
		http.Error(w, "empty prompt", http.StatusBadRequest)
		return
	}

	streamID := rand.Text()
	a.mu.Lock()
	a.pending[streamID] = pendingPrompt{session: sess.ID, prompt: prompt}
	a.mu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="msg user">%s</div>
<div class="msg bot" hx-ext="sse" sse-connect="/s/%s/stream/%s" sse-swap="token" sse-close="done" hx-swap="beforeend"></div>`,
		html.EscapeString(prompt), sess.ID, streamID)
}

// stream runs the model for a pending prompt and emits each token as an SSE
// "token" event, finishing with a "done" event. If the model calls a tool,
// the tool runs and the model is asked again with the result. Turns in one
// session are serialized by the session lock.
func (a *app) stream(w http.ResponseWriter, r *http.Request) {
	sess := a.session(w, r)
	if sess == nil {
		return
	}

	a.mu.Lock()
	p, ok := a.pending[r.PathValue("id")]
	delete(a.pending, r.PathValue("id"))
	a.mu.Unlock()

	if !ok || p.session != sess.ID {
		http.NotFound(w, r)
		return
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

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
		sess.Messages = []model.D{model.TextMessage(model.RoleSystem, systemPrompt)}
	}
	sess.Messages = append(sess.Messages, model.TextMessage(model.RoleUser, p.prompt))
	sess.Entries = append(sess.Entries, Entry{Role: model.RoleUser, Blocks: []Block{{Text: p.prompt}}})

	// reply mirrors the bot bubble: notes and text in the order they appear.
	var reply Entry
	reply.Role = model.RoleAssistant
	addText := func(tok string) {
		if n := len(reply.Blocks); n > 0 && !reply.Blocks[n-1].Note {
			reply.Blocks[n-1].Text += tok
		} else {
			reply.Blocks = append(reply.Blocks, Block{Text: tok})
		}
		send("token", html.EscapeString(tok))
	}
	addNote := func(note string) {
		reply.Blocks = append(reply.Blocks, Block{Note: true, Text: note})
		send("token", `<div class="note">`+html.EscapeString(note)+`</div>`)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	for range maxToolTurns {
		answer, toolCalls, err := a.modelTurn(ctx, sess.Messages, addText)
		if err != nil {
			addNote("error: " + err.Error())
			break
		}

		if len(toolCalls) == 0 {
			sess.Messages = append(sess.Messages, model.TextMessage(model.RoleAssistant, answer))
			break
		}

		sess.Messages = append(sess.Messages, toolCallMessage(toolCalls))
		for _, tc := range toolCalls {
			result := a.callTool(sess, tc)
			addNote(result)
			sess.Messages = append(sess.Messages, model.D{
				"role":         model.RoleTool,
				"name":         tc.Function.Name,
				"tool_call_id": tc.ID,
				"content":      result,
			})
		}
	}
	sess.Entries = append(sess.Entries, reply)

	if err := sess.save(); err != nil {
		log.Printf("save session %s: %v", sess.ID, err)
	}

	// Refresh both side panels out of band: chat.md and new documents show
	// up on the right, a first message adds the session on the left.
	send("token", fmt.Sprintf(`<ul hx-swap-oob="innerHTML:#docs">%s</ul><ul hx-swap-oob="innerHTML:#sessions">%s</ul>`,
		docItems(sess), a.sessionItems(sess.ID)))
	send("done", "")
}

// modelTurn runs one model call, passing content tokens to onToken. It
// returns the full answer, or the tool calls if the model requested any.
func (a *app) modelTurn(ctx context.Context, conversation []model.D, onToken func(string)) (string, []model.ResponseToolCall, error) {
	ch, err := a.krn.ChatStreaming(ctx, model.D{
		"messages":        conversation,
		"tools":           tools,
		"enable_thinking": false,
		"temperature":     0.7,
		"max_tokens":      2048,
	})
	if err != nil {
		return "", nil, err
	}

	var answer strings.Builder
	var toolCalls []model.ResponseToolCall
	for resp := range ch {
		if len(resp.Choices) == 0 {
			continue
		}
		choice := resp.Choices[0]
		switch choice.FinishReason() {
		case model.FinishReasonError:
			return "", nil, errors.New(choice.Delta.Content)
		case model.FinishReasonTool:
			toolCalls = choice.Message.ToolCalls
		default:
			if tok := choice.Delta.Content; tok != "" {
				answer.WriteString(tok)
				onToken(tok)
			}
		}
	}
	return answer.String(), toolCalls, nil
}

// toolCallMessage is the assistant message recording which tools it called.
func toolCallMessage(toolCalls []model.ResponseToolCall) model.D {
	docs := make([]model.D, 0, len(toolCalls))
	for _, tc := range toolCalls {
		args, _ := json.Marshal(tc.Function.Arguments)
		docs = append(docs, model.D{
			"id":   tc.ID,
			"type": "function",
			"function": model.D{
				"name":      tc.Function.Name,
				"arguments": string(args),
			},
		})
	}
	return model.D{"role": model.RoleAssistant, "tool_calls": docs}
}

// callTool executes a tool call in the session's directory and returns the
// result, which goes back to the model and is shown as a note in the chat.
func (a *app) callTool(sess *Session, tc model.ResponseToolCall) string {
	if tc.Function.Name != "create_document" {
		return "error: unknown tool " + tc.Function.Name
	}

	filename, _ := tc.Function.Arguments["filename"].(string)
	content, _ := tc.Function.Arguments["content"].(string)
	name := cleanFilename(filename)
	if name == transcript {
		return "error: " + transcript + " is reserved for the chat transcript, choose another filename"
	}

	root, err := sess.docs()
	if err != nil {
		return "error: " + err.Error()
	}
	defer root.Close()

	if err := root.WriteFile(name, []byte(content), 0o600); err != nil {
		return "error: " + err.Error()
	}

	log.Printf("session %s: created document %s (%d bytes)", sess.ID, name, len(content))
	return "Created document " + name
}

func (a *app) downloadDoc(w http.ResponseWriter, r *http.Request) {
	sess := a.session(w, r)
	if sess == nil {
		return
	}

	name := r.PathValue("name")
	if strings.HasPrefix(name, ".") {
		http.NotFound(w, r)
		return
	}

	root, err := os.OpenRoot(sess.dir)
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
func docItems(sess *Session) string {
	var b strings.Builder
	for _, name := range sess.docNames() {
		fmt.Fprintf(&b, `<li><a href="/s/%s/docs/%s" download>%s</a></li>`,
			sess.ID, html.EscapeString(url.PathEscape(name)), html.EscapeString(name))
	}
	return b.String()
}

// sessionItems renders the sessions panel's list items, marking the current one.
func (a *app) sessionItems(current string) string {
	var b strings.Builder
	for _, s := range a.store.list() {
		class := ""
		if s.ID == current {
			class = ` class="current"`
		}
		fmt.Fprintf(&b, `<li%s><a href="/s/%s" title="%s">%s</a></li>`,
			class, s.ID, s.Created.Format(time.DateTime), html.EscapeString(s.Title))
	}
	return b.String()
}

// cleanFilename turns a model-supplied name into a safe, flat file name.
func cleanFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return "document.txt"
	}
	return name
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
