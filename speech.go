package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ardanlabs/kronk/sdk/bucky"
	buckymodel "github.com/ardanlabs/kronk/sdk/bucky/model"
	buckylibs "github.com/ardanlabs/kronk/sdk/tools/bucky/libs"
	buckymodels "github.com/ardanlabs/kronk/sdk/tools/bucky/models"
)

// maxRecording caps an uploaded recording: 16-bit mono at 48 kHz is about
// 5.5 MB per minute.
const maxRecording = 32 << 20

// loadSpeech downloads whisper.cpp and the speech model on first run, then
// loads it.
func loadSpeech(modelSource string) (*bucky.Bucky, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	lb, err := buckylibs.New(buckylibs.WithDetect(ctx, bucky.FmtLogger), buckylibs.WithValidation(true))
	if err != nil {
		return nil, err
	}
	if _, err := lb.Download(ctx, bucky.FmtLogger); err != nil {
		return nil, fmt.Errorf("install whisper.cpp: %w", err)
	}
	if err := bucky.Init(bucky.WithLibPath(lb.LibsPath())); err != nil {
		return nil, fmt.Errorf("init bucky: %w", err)
	}

	mdls, err := buckymodels.New()
	if err != nil {
		return nil, err
	}
	mp, err := mdls.Download(ctx, bucky.FmtLogger, modelSource)
	if err != nil {
		return nil, fmt.Errorf("install speech model: %w", err)
	}
	if len(mp.ModelFiles) == 0 {
		return nil, fmt.Errorf("install speech model: no model files on disk")
	}

	return bucky.New(buckymodel.WithModelPath(mp.ModelFiles[0]), buckymodel.WithUseGPU(true))
}

// transcribe turns a recorded WAV from the browser into text for the
// prompt line. WAV is decoded in-process, so no ffmpeg is needed.
func (a *app) transcribe(w http.ResponseWriter, r *http.Request) {
	if a.session(w, r) == nil {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()

	start := time.Now()
	res, err := a.speech.TranscribeFile(ctx, http.MaxBytesReader(w, r.Body, maxRecording))
	if err != nil {
		log.Printf("transcribe: %v", err)
		http.Error(w, "transcription failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	text := strings.TrimSpace(res.Text)
	log.Printf("transcribed %.1fs of audio in %s: %q", res.Duration, time.Since(start).Round(time.Millisecond), text)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, text)
}
