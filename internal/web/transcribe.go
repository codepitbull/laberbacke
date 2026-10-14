package web

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// maxRecording caps an uploaded recording: 16-bit mono at 48 kHz is about
// 5.5 MB per minute.
const maxRecording = 32 << 20

// transcribe turns a recorded WAV from the browser into text for the
// prompt line. WAV is decoded in-process, so no ffmpeg is needed.
func (s *Server) transcribe(w http.ResponseWriter, r *http.Request) {
	if s.session(w, r) == nil {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()

	start := time.Now()
	res, err := s.Speech.TranscribeFile(ctx, http.MaxBytesReader(w, r.Body, maxRecording))
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
