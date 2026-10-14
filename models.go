package main

import (
	"context"
	"encoding/json"
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
	inst, err := locate(cfg, offline)
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
// local disk, and only accepts complete models (see complete).
//
// Offline, the kronk dir is a staged copy without model indexes, so they are
// rebuilt from the files present (hashing the chat model). Otherwise the
// index Kronk's downloader keeps is trusted, as a rebuild would also pick up
// interrupted downloads.
func locate(cfg Config, offline bool) (installation, error) {
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
	// A fresh index only marks a model validated after checking its SHA-256
	// against the sha/ pointer file, so a staged copy is hashed once here.
	mp, err := lookup(offline, func() (models.Path, error) { return mdls.FullPath(cfg.Model) },
		func() error { return mdls.BuildIndex(kronk.FmtLogger, true) })
	if err != nil {
		return inst, fmt.Errorf("model %s %w", cfg.Model, errMissing)
	}
	if err := verified(mp.ModelFiles); err != nil {
		return inst, fmt.Errorf("model %s: %v, %w", cfg.Model, err, errMissing)
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
	bmp, err := lookup(offline, func() (models.Path, error) { return bmdls.FullPath(cfg.SpeechModel) },
		func() error { return bmdls.BuildIndex(bucky.FmtLogger, false) })
	if err != nil || len(bmp.ModelFiles) == 0 {
		return inst, fmt.Errorf("speech model %s %w", cfg.SpeechModel, errMissing)
	}
	inst.speechModel = bmp.ModelFiles[0]

	return inst, nil
}

// lookup finds a complete model in the index. With reindex allowed, an
// unknown model triggers one index rebuild. models.Path and buckymodels.Path
// are the same type.
func lookup(allowReindex bool, find func() (models.Path, error), reindex func() error) (models.Path, error) {
	for attempt := range 2 {
		if attempt == 1 {
			if !allowReindex {
				break
			}
			if err := reindex(); err != nil {
				return models.Path{}, err
			}
		}
		mp, err := find()
		if err == nil && mp.Validated && filesExist(mp.ModelFiles) {
			return mp, nil
		}
	}
	return models.Path{}, errMissing
}

// verified checks each model file against the sha/<file>.verified marker
// Kronk writes after checking a download's hash. A file without that
// marker, or of another size, is an interrupted or unverified download.
// Kronk downloads model files in place, so a partial file looks like a
// model otherwise.
func verified(files []string) error {
	for _, f := range files {
		dir, name := filepath.Split(f)
		data, err := os.ReadFile(filepath.Join(dir, "sha", name+".verified"))
		if err != nil {
			return fmt.Errorf("%s is not verified", name)
		}
		var marker struct {
			Size int64 `json:"size"`
		}
		if err := json.Unmarshal(data, &marker); err != nil {
			return fmt.Errorf("%s: bad verification marker: %w", name, err)
		}
		info, err := os.Stat(f)
		if err != nil {
			return err
		}
		if info.Size() != marker.Size {
			return fmt.Errorf("%s is %d bytes, expected %d", name, info.Size(), marker.Size)
		}
	}
	return nil
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
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
	mp, err := retry(ctx, func() (models.Path, error) { return mdls.Download(ctx, kronk.FmtLogger, cfg.Model) })
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
	bmp, err := retry(ctx, func() (models.Path, error) { return bmdls.Download(ctx, bucky.FmtLogger, cfg.SpeechModel) })
	if err != nil {
		return inst, fmt.Errorf("install speech model %s: %w", cfg.SpeechModel, err)
	}
	if len(bmp.ModelFiles) == 0 {
		return inst, fmt.Errorf("install speech model %s: no model files on disk", cfg.SpeechModel)
	}
	inst.speechModel = bmp.ModelFiles[0]

	return inst, nil
}

// retry runs a model download up to three times. Hugging Face sometimes
// drops long downloads (HTTP/2 GOAWAY); Kronk restarts the file on retry.
func retry(ctx context.Context, download func() (models.Path, error)) (models.Path, error) {
	const attempts = 3
	var err error
	for i := 1; i <= attempts; i++ {
		var mp models.Path
		if mp, err = download(); err == nil {
			return mp, nil
		}
		if i == attempts || ctx.Err() != nil {
			break
		}
		wait := time.Duration(i) * 10 * time.Second
		log.Printf("download attempt %d/%d failed, retrying in %s: %v", i, attempts, wait, err)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return models.Path{}, ctx.Err()
		}
	}
	return models.Path{}, err
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
//
// Staged files must be readable by any user, since the container may run
// with the host's uid. Files that are not (Kronk writes its .verified
// markers as 0600) are copied rather than linked and made world-readable,
// so the original's mode is left alone.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}

	worldReadable := info.Mode().Perm()&0o004 != 0
	if worldReadable {
		if err := os.Link(src, dst); err == nil {
			return nil
		}
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(dst, info.Mode().Perm()|0o444); err != nil {
		return err
	}
	// Keep the mtime: Kronk's .verified markers record it to skip rehashing.
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

// loadModel loads the installed chat model.
func loadModel(inst installation) (*kronk.Kronk, error) {
	if err := kronk.Init(kronk.WithLibPath(inst.chatLibs)); err != nil {
		return nil, fmt.Errorf("init kronk: %w", err)
	}
	// AutoTune can pick a q8_0 V cache together with Flash Attention off
	// (seen for gpt-oss on CPU), which Kronk then rejects. An f16 V cache is
	// valid either way; AutoTune still chooses everything else.
	return kronk.New(
		model.WithModelFiles(inst.chatModel),
		model.WithAutoTune(true),
		model.WithCacheTypeV(model.GGMLTypeF16),
	)
}

// loadSpeech loads the installed whisper model.
func loadSpeech(inst installation) (*bucky.Bucky, error) {
	if err := bucky.Init(bucky.WithLibPath(inst.speechLibs)); err != nil {
		return nil, fmt.Errorf("init bucky: %w", err)
	}
	return bucky.New(buckymodel.WithModelPath(inst.speechModel), buckymodel.WithUseGPU(true))
}
