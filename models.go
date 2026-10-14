package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ardanlabs/kronk/sdk/bucky"
	buckymodel "github.com/ardanlabs/kronk/sdk/bucky/model"
	"github.com/ardanlabs/kronk/sdk/kronk"
	"github.com/ardanlabs/kronk/sdk/kronk/model"
	buckylibs "github.com/ardanlabs/kronk/sdk/tools/bucky/libs"
	buckymodels "github.com/ardanlabs/kronk/sdk/tools/bucky/models"
	"github.com/ardanlabs/kronk/sdk/tools/libs"
	"github.com/ardanlabs/kronk/sdk/tools/models"
)

// installation is where the downloaded native libraries and models live.
type installation struct {
	chatLibs    string
	chatModel   []string
	speechLibs  string
	speechModel string
}

// install downloads llama.cpp, whisper.cpp and both models into kronkDir,
// skipping whatever is already there. It loads nothing. An empty kronkDir
// means KRONK_BASE_PATH or ~/.kronk.
func install(cfg Config) (installation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	var inst installation

	lb, err := libs.New(libs.WithBasePath(cfg.KronkDir), libs.WithDetect(ctx, kronk.FmtLogger), libs.WithValidation(true))
	if err != nil {
		return inst, err
	}
	if _, err := lb.Download(ctx, kronk.FmtLogger); err != nil {
		return inst, fmt.Errorf("install llama.cpp: %w", err)
	}
	inst.chatLibs = lb.LibsPath()

	mdls, err := models.NewWithPaths(cfg.KronkDir)
	if err != nil {
		return inst, err
	}
	mp, err := mdls.Download(ctx, kronk.FmtLogger, cfg.Model)
	if err != nil {
		return inst, fmt.Errorf("install model %s: %w", cfg.Model, err)
	}
	inst.chatModel = mp.ModelFiles

	blb, err := buckylibs.New(buckylibs.WithBasePath(cfg.KronkDir), buckylibs.WithDetect(ctx, bucky.FmtLogger), buckylibs.WithValidation(true))
	if err != nil {
		return inst, err
	}
	if _, err := blb.Download(ctx, bucky.FmtLogger); err != nil {
		return inst, fmt.Errorf("install whisper.cpp: %w", err)
	}
	inst.speechLibs = blb.LibsPath()

	bmdls, err := buckymodels.NewWithPaths(cfg.KronkDir)
	if err != nil {
		return inst, err
	}
	bmp, err := bmdls.Download(ctx, bucky.FmtLogger, cfg.SpeechModel)
	if err != nil {
		return inst, fmt.Errorf("install speech model %s: %w", cfg.SpeechModel, err)
	}
	if len(bmp.ModelFiles) == 0 {
		return inst, fmt.Errorf("install speech model %s: no model files on disk", cfg.SpeechModel)
	}
	inst.speechModel = bmp.ModelFiles[0]

	return inst, nil
}

func (inst installation) log() {
	log.Printf("llama.cpp:    %s", inst.chatLibs)
	log.Printf("chat model:   %v", inst.chatModel)
	log.Printf("whisper.cpp:  %s", inst.speechLibs)
	log.Printf("speech model: %s", inst.speechModel)
}

// loadModel loads the installed chat model.
func loadModel(inst installation) (*kronk.Kronk, error) {
	if err := kronk.Init(kronk.WithLibPath(inst.chatLibs)); err != nil {
		return nil, fmt.Errorf("init kronk: %w", err)
	}
	return kronk.New(model.WithModelFiles(inst.chatModel), model.WithAutoTune(true))
}

// loadSpeech loads the installed whisper model.
func loadSpeech(inst installation) (*bucky.Bucky, error) {
	if err := bucky.Init(bucky.WithLibPath(inst.speechLibs)); err != nil {
		return nil, fmt.Errorf("init bucky: %w", err)
	}
	return bucky.New(buckymodel.WithModelPath(inst.speechModel), buckymodel.WithUseGPU(true))
}
