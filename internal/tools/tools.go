// Package tools holds the functions the model may call and runs them.
package tools

import (
	"context"
	"log"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
)

// Env is what a tool may touch while it runs.
type Env struct {
	// Dir is the directory the tool writes its artifacts into.
	Dir string
}

// Tool is one callable function.
type Tool interface {
	// Name is the function name the model uses.
	Name() string

	// Definition describes the tool to the model, in the OpenAI format.
	Definition() model.D

	// Call runs the tool. The result goes back to the model and is shown
	// to the user as a note, so errors are returned as text too.
	Call(ctx context.Context, env Env, args model.ToolCallArguments) string
}

// Registry is the set of tools offered to the model.
type Registry struct {
	tools []Tool
}

func NewRegistry(tools ...Tool) *Registry {
	return &Registry{tools: tools}
}

// Definitions returns the tool list for a chat request.
func (r *Registry) Definitions() []model.D {
	defs := make([]model.D, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	return defs
}

// Call runs the tool the model asked for.
func (r *Registry) Call(ctx context.Context, env Env, tc model.ResponseToolCall) string {
	log.Printf("tool call %s(%v)", tc.Function.Name, tc.Function.Arguments)
	for _, t := range r.tools {
		if t.Name() == tc.Function.Name {
			return t.Call(ctx, env, tc.Function.Arguments)
		}
	}
	return "error: unknown tool " + tc.Function.Name
}
