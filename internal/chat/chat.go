// Package chat runs one assistant reply: it streams model output and loops
// through tool calls until the model answers in plain text.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/ardanlabs/kronk/sdk/kronk"
	"github.com/ardanlabs/kronk/sdk/kronk/model"

	"kronkplay/internal/tools"
)

// maxToolTurns bounds the model -> tool -> model loop for one reply.
const maxToolTurns = 3

type Engine struct {
	krn          *kronk.Kronk
	tools        *tools.Registry
	systemPrompt string
}

func New(krn *kronk.Kronk, tools *tools.Registry, systemPrompt string) *Engine {
	return &Engine{krn: krn, tools: tools, systemPrompt: systemPrompt}
}

// ModelID names the loaded model.
func (e *Engine) ModelID() string { return e.krn.ModelID() }

// Start returns the opening messages of a new conversation.
func (e *Engine) Start() []model.D {
	return []model.D{model.TextMessage(model.RoleSystem, e.systemPrompt)}
}

// Output receives a reply as it is produced.
type Output struct {
	Token func(text string) // streamed answer text
	Note  func(text string) // tool results and errors
}

// Reply answers the last message in history and returns history extended
// with the assistant and tool messages.
func (e *Engine) Reply(ctx context.Context, env tools.Env, history []model.D, out Output) []model.D {
	for range maxToolTurns {
		answer, toolCalls, err := e.turn(ctx, history, out.Token)
		if err != nil {
			out.Note("error: " + err.Error())
			return history
		}

		if len(toolCalls) == 0 {
			return append(history, model.TextMessage(model.RoleAssistant, answer))
		}

		history = append(history, toolCallMessage(toolCalls))
		for _, tc := range toolCalls {
			result := e.tools.Call(ctx, env, tc)
			out.Note(result)
			history = append(history, model.D{
				"role":         model.RoleTool,
				"name":         tc.Function.Name,
				"tool_call_id": tc.ID,
				"content":      result,
			})
		}
	}
	return history
}

// turn runs one model call, passing content tokens to onToken. It returns
// the full answer, or the tool calls if the model requested any.
func (e *Engine) turn(ctx context.Context, history []model.D, onToken func(string)) (string, []model.ResponseToolCall, error) {
	ch, err := e.krn.ChatStreaming(ctx, model.D{
		"messages":        history,
		"tools":           e.tools.Definitions(),
		"enable_thinking": false,
		"temperature":     0.7,
		"max_tokens":      2048,
	})
	if err != nil {
		return "", nil, err
	}

	var answer strings.Builder
	var toolCalls []model.ResponseToolCall
	for resp := range ch {
		if len(resp.Choices) == 0 {
			continue
		}
		choice := resp.Choices[0]
		switch choice.FinishReason() {
		case model.FinishReasonError:
			return "", nil, errors.New(choice.Delta.Content)
		case model.FinishReasonTool:
			toolCalls = choice.Message.ToolCalls
		default:
			if tok := choice.Delta.Content; tok != "" {
				answer.WriteString(tok)
				onToken(tok)
			}
		}
	}
	return answer.String(), toolCalls, nil
}

// toolCallMessage is the assistant message recording which tools it called.
func toolCallMessage(toolCalls []model.ResponseToolCall) model.D {
	docs := make([]model.D, 0, len(toolCalls))
	for _, tc := range toolCalls {
		args, _ := json.Marshal(tc.Function.Arguments)
		docs = append(docs, model.D{
			"id":   tc.ID,
			"type": "function",
			"function": model.D{
				"name":      tc.Function.Name,
				"arguments": string(args),
			},
		})
	}
	return model.D{"role": model.RoleAssistant, "tool_calls": docs}
}
