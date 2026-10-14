package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ardanlabs/kronk/sdk/bucky"
	buckymodel "github.com/ardanlabs/kronk/sdk/bucky/model"
	"github.com/ardanlabs/kronk/sdk/kronk"
	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/ardanlabs/kronk/sdk/tools/bucky/libs"
	buckymodels "github.com/ardanlabs/kronk/sdk/tools/bucky/models"
	kronklibs "github.com/ardanlabs/kronk/sdk/tools/libs"
	"github.com/ardanlabs/kronk/sdk/tools/models"
)

// loadModel downloads llama.cpp and the chat model on first run, then loads it.
func loadModel(modelSource string) (*kronk.Kronk, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	lb, err := kronklibs.New(kronklibs.WithDetect(ctx, kronk.FmtLogger), kronklibs.WithValidation(true))
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

// loadSpeech downloads whisper.cpp and the speech model on first run, then
// loads it.
func loadSpeech(modelSource string) (*bucky.Bucky, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	lb, err := libs.New(libs.WithDetect(ctx, bucky.FmtLogger), libs.WithValidation(true))
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
