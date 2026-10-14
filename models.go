package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/ardanlabs/kronk/sdk/bucky"
	buckymodel "github.com/ardanlabs/kronk/sdk/bucky/model"
	"github.com/ardanlabs/kronk/sdk/kronk"
	"github.com/ardanlabs/kronk/sdk/kronk/model"
	buckylibs "github.com/ardanlabs/kronk/sdk/tools/bucky/libs"
	buckymodels "github.com/ardanlabs/kronk/sdk/tools/bucky/models"
	"github.com/ardanlabs/kronk/sdk/tools/defaults"
	"github.com/ardanlabs/kronk/sdk/tools/libs"
	"github.com/ardanlabs/kronk/sdk/tools/models"
)

// installation is where the native libraries and models live on disk.
//
// The library folders are per platform. KRONK_OS, KRONK_ARCH and
// KRONK_PROCESSOR select another platform than the host, e.g. to prepare
// Linux libraries for a container on a Mac.
type installation struct {
	chatLibs    string
	chatModel   []string
	speechLibs  string
	speechModel string
}

var errMissing = errors.New("not in kronk dir")

// resolve returns the installation for cfg. Everything already in the kronk
// dir is used as is, without network access; only missing parts are
// downloaded. With offline set, a missing part is an error instead.
func resolve(cfg Config, offline bool) (installation, error) {
	inst, err := locate(cfg)
	if err == nil {
		log.Print("all libraries and models found in kronk dir")
		return inst, nil
	}
	if offline || !errors.Is(err, errMissing) {
		return installation{}, err
	}

	log.Printf("%v, downloading", err)
	return install(cfg)
}

// locate finds the libraries and models in the kronk dir. It only reads the
// local disk. A stale or missing model index (e.g. after the files were
// copied from elsewhere) is rebuilt from the files present.
func locate(cfg Config) (installation, error) {
	var inst installation

	lb, err := libs.New(libs.WithBasePath(cfg.KronkDir))
	if err != nil {
		return inst, err
	}
	if _, err := lb.InstalledVersion(); err != nil {
		return inst, fmt.Errorf("llama.cpp for %s/%s/%s %w", lb.OS(), lb.Arch(), lb.Processor(), errMissing)
	}
	inst.chatLibs = lb.LibsPath()

	mdls, err := models.NewWithPaths(cfg.KronkDir)
	if err != nil {
		return inst, err
	}
	mp, err := lookup(func() (models.Path, error) { return mdls.FullPath(cfg.Model) },
		func() error { return mdls.BuildIndex(kronk.FmtLogger, false) })
	if err != nil {
		return inst, fmt.Errorf("model %s %w", cfg.Model, errMissing)
	}
	inst.chatModel = mp.ModelFiles

	blb, err := buckylibs.New(buckylibs.WithBasePath(cfg.KronkDir))
	if err != nil {
		return inst, err
	}
	if _, err := blb.InstalledVersion(); err != nil {
		return inst, fmt.Errorf("whisper.cpp for %s/%s/%s %w", blb.OS(), blb.Arch(), blb.Processor(), errMissing)
	}
	inst.speechLibs = blb.LibsPath()

	bmdls, err := buckymodels.NewWithPaths(cfg.KronkDir)
	if err != nil {
		return inst, err
	}
	bmp, err := lookup(func() (models.Path, error) { return bmdls.FullPath(cfg.SpeechModel) },
		func() error { return bmdls.BuildIndex(bucky.FmtLogger, false) })
	if err != nil || len(bmp.ModelFiles) == 0 {
		return inst, fmt.Errorf("speech model %s %w", cfg.SpeechModel, errMissing)
	}
	inst.speechModel = bmp.ModelFiles[0]

	return inst, nil
}

// lookup finds a model in the index, rebuilding the index once if the model
// is unknown or its files are gone. models.Path and buckymodels.Path are the
// same type.
func lookup(find func() (models.Path, error), reindex func() error) (models.Path, error) {
	for attempt := range 2 {
		if attempt == 1 {
			if err := reindex(); err != nil {
				return models.Path{}, err
			}
		}
		mp, err := find()
		if err == nil && filesExist(mp.ModelFiles) {
			return mp, nil
		}
	}
	return models.Path{}, errMissing
}

func filesExist(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			return false
		}
	}
	return true
}

// install downloads whatever is missing from the kronk dir and verifies the
// libraries. It loads nothing.
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

// stage copies exactly what this installation uses into dest, keeping the
// kronk dir layout: both library folders, the model files and their
// verification markers. The model indexes are left out because they hold
// absolute paths; locate rebuilds them where the copy ends up.
func (inst installation) stage(kronkDir, dest string) error {
	root := defaults.BaseDir(kronkDir)

	var files []string
	for _, f := range append(inst.chatModel, inst.speechModel) {
		files = append(files, f)
		dir, name := filepath.Split(f)
		for _, marker := range []string{filepath.Join(dir, "sha", name), filepath.Join(dir, "sha", name+".verified")} {
			if _, err := os.Stat(marker); err == nil {
				files = append(files, marker)
			}
		}
	}
	for _, dir := range []string{inst.chatLibs, inst.speechLibs} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err == nil && (d.Type().IsRegular() || d.Type()&fs.ModeSymlink != 0) {
				files = append(files, path)
			}
			return err
		})
		if err != nil {
			return err
		}
	}

	for _, src := range files {
		rel, err := filepath.Rel(root, src)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("stage: %s is outside the kronk dir %s", src, root)
		}
		if err := copyFile(src, filepath.Join(dest, rel)); err != nil {
			return fmt.Errorf("stage %s: %w", rel, err)
		}
	}
	log.Printf("staged %d files into %s", len(files), dest)
	return nil
}

// copyFile hard-links src to dst when both are on the same file system and
// copies it otherwise. Symlinks (e.g. libggml.so -> libggml.so.0) are
// recreated as symlinks.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	if info, err := os.Lstat(src); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}

	if err := os.Link(src, dst); err == nil {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
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
