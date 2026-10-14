package main

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	defaultModel       = "unsloth/Qwen3-0.6B-Q8_0"
	defaultSpeechModel = "base.en"
)

// Config is read from config.yml.
type Config struct {
	// Model is any source Kronk can download: a "provider/modelID", a bare
	// model id, or a HuggingFace URL.
	Model string `yaml:"model"`

	// SpeechModel is the whisper model Bucky downloads for speech input,
	// e.g. "tiny", "base.en" or "large-v3".
	SpeechModel string `yaml:"speech_model"`
}

// loadConfig reads the config file at path. A missing file yields the
// defaults; unknown keys are rejected so typos do not go unnoticed.
func loadConfig(path string) (Config, error) {
	cfg := Config{Model: defaultModel, SpeechModel: defaultSpeechModel}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}

	cfg.Model = cmp.Or(strings.TrimSpace(cfg.Model), defaultModel)
	cfg.SpeechModel = cmp.Or(strings.TrimSpace(cfg.SpeechModel), defaultSpeechModel)
	return cfg, nil
}
