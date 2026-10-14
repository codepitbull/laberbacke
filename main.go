// A minimal web chat on top of Kronk, with document tools and speech input.
//
// Every chat is a session with a UUID, reachable at /s/{uuid}. Its directory
// sessions/{uuid}/ holds the documents the model created plus a chat.md
// transcript.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"kronkplay/internal/chat"
	"kronkplay/internal/session"
	"kronkplay/internal/tools"
	"kronkplay/internal/web"
)

const systemPrompt = `You are a helpful assistant. Answer briefly and clearly.
When the user asks you to create, write or save a document or file, call the
create_document tool with a short filename and the full document content.
Otherwise just answer in the chat.`

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

	store, err := session.NewStore(*dataDir)
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

	registry := tools.NewRegistry(
		tools.CreateDocument{Reserved: []string{session.TranscriptFile}},
	)

	srv := web.New(web.Config{
		Chat:      chat.New(krn, registry, systemPrompt),
		Speech:    speech,
		Sessions:  store,
		ModelName: cfg.Model,
	})

	return serve(*addr, srv.Handler(), store.Root())
}

// serve runs the HTTP server until SIGINT or SIGTERM.
func serve(addr string, handler http.Handler, dataDir string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{Addr: addr, Handler: handler}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	abs, _ := filepath.Abs(dataDir)
	log.Printf("listening on http://%s (sessions in %s)", addr, abs)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
