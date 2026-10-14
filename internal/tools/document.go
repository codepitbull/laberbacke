package tools

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
)

// CreateDocument writes a text document into the session directory.
type CreateDocument struct {
	// Reserved are file names the model may not write, e.g. the transcript.
	Reserved []string
}

func (CreateDocument) Name() string { return "create_document" }

func (CreateDocument) Definition() model.D {
	return model.D{
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
	}
}

func (t CreateDocument) Call(_ context.Context, env Env, args model.ToolCallArguments) string {
	filename, _ := args["filename"].(string)
	content, _ := args["content"].(string)

	name := CleanFilename(filename)
	if slices.Contains(t.Reserved, name) {
		return "error: " + name + " is reserved, choose another filename"
	}

	if err := os.MkdirAll(env.Dir, 0o700); err != nil {
		return "error: " + err.Error()
	}
	root, err := os.OpenRoot(env.Dir)
	if err != nil {
		return "error: " + err.Error()
	}
	defer root.Close()

	if err := root.WriteFile(name, []byte(content), 0o600); err != nil {
		return "error: " + err.Error()
	}

	log.Printf("created document %s/%s (%d bytes)", filepath.Base(env.Dir), name, len(content))
	return "Created document " + name
}

// CleanFilename turns a model-supplied name into a safe, flat file name.
func CleanFilename(name string) string {
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
