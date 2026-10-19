package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Message roles of a conversation with the model.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is one turn of a conversation: the instructions, the question, a model reply (text or
// tool calls) or the result of one tool call.
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

// ToolCall is the model asking to run a tool with arguments.
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]string
}

// Tool is a function the model may call: a read-only report query. Parameters is its JSON schema.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// Request is a conversation and the tools offered for the next reply.
type Request struct {
	Messages []Message
	Tools    []Tool
}

// Reply is the model's next turn: tool calls to run, or the final answer in Content.
type Reply struct {
	Content   string
	ToolCalls []ToolCall
}

// ModelClient is the language model behind the Q&A. It sees only the conversation the service
// builds: the question, the tool catalogue and report results with personal columns left out.
type ModelClient interface {
	// Model names the model, as the query log records it.
	Model() string
	Complete(ctx context.Context, req Request) (Reply, error)
}

// HTTPModelConfig configures an OpenAI-compatible chat completions endpoint.
type HTTPModelConfig struct {
	// BaseURL is the API root; requests go to BaseURL + "/chat/completions".
	BaseURL *url.URL
	APIKey  string
	Model   string
	// Client defaults to a plain client; CallTimeout bounds each call either way.
	Client *http.Client
	// CallTimeout bounds one model call; zero means 20 s.
	CallTimeout time.Duration
}

// defaultCallTimeout bounds one model call, so a slow provider leaves the question's own deadline
// room for another step.
const defaultCallTimeout = 20 * time.Second

// maxModelResponse bounds a model response body.
const maxModelResponse = 1 << 20

// NewHTTPModel returns the model at cfg, or nil when no endpoint or model is configured, which
// leaves the Q&A disabled.
func NewHTTPModel(cfg HTTPModelConfig) ModelClient {
	if cfg.BaseURL == nil || cfg.Model == "" {
		return nil
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{}
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = defaultCallTimeout
	}
	endpoint := strings.TrimSuffix(cfg.BaseURL.String(), "/") + "/chat/completions"
	return &httpModel{endpoint: endpoint, apiKey: cfg.APIKey, model: cfg.Model, client: cfg.Client, timeout: cfg.CallTimeout}
}

type httpModel struct {
	endpoint, apiKey, model string
	client                  *http.Client
	timeout                 time.Duration
}

func (m *httpModel) Model() string { return m.model }

type wireFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Arguments   string         `json:"arguments,omitempty"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireRequest struct {
	Model       string        `json:"model"`
	Messages    []wireMessage `json:"messages"`
	Tools       []wireTool    `json:"tools,omitempty"`
	Temperature float64       `json:"temperature"`
}

type wireResponse struct {
	Choices []struct {
		Message wireMessage `json:"message"`
	} `json:"choices"`
}

func (m *httpModel) Complete(ctx context.Context, req Request) (Reply, error) {
	body := wireRequest{Model: m.model, Messages: make([]wireMessage, len(req.Messages))}
	for i, msg := range req.Messages {
		w := wireMessage{Role: msg.Role, ToolCallID: msg.ToolCallID}
		if msg.Content != "" || len(msg.ToolCalls) == 0 {
			content := msg.Content
			w.Content = &content
		}
		for _, call := range msg.ToolCalls {
			args, err := json.Marshal(call.Arguments)
			if err != nil {
				return Reply{}, fmt.Errorf("analytics: encode tool arguments: %w", err)
			}
			w.ToolCalls = append(w.ToolCalls, wireToolCall{
				ID: call.ID, Type: "function", Function: wireFunction{Name: call.Name, Arguments: string(args)},
			})
		}
		body.Messages[i] = w
	}
	for _, t := range req.Tools {
		body.Tools = append(body.Tools, wireTool{
			Type: "function", Function: wireFunction{Name: t.Name, Description: t.Description, Parameters: t.Parameters},
		})
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Reply{}, fmt.Errorf("analytics: encode model request: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(raw))
	if err != nil {
		return Reply{}, fmt.Errorf("analytics: model request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	resp, err := m.client.Do(httpReq)
	if err != nil {
		return Reply{}, fmt.Errorf("analytics: call model: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxModelResponse+1))
	if err != nil {
		return Reply{}, fmt.Errorf("analytics: read model response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Reply{}, fmt.Errorf("analytics: model answered HTTP %d", resp.StatusCode)
	}
	if len(data) > maxModelResponse {
		return Reply{}, errors.New("analytics: model response is too large")
	}
	var out wireResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return Reply{}, fmt.Errorf("analytics: decode model response: %w", err)
	}
	if len(out.Choices) == 0 {
		return Reply{}, errors.New("analytics: model response has no choice")
	}
	msg := out.Choices[0].Message
	reply := Reply{}
	if msg.Content != nil {
		reply.Content = *msg.Content
	}
	for _, call := range msg.ToolCalls {
		args, err := decodeArguments(call.Function.Arguments)
		if err != nil {
			return Reply{}, err
		}
		reply.ToolCalls = append(reply.ToolCalls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: args})
	}
	return reply, nil
}

// decodeArguments reads the JSON object of a tool call's arguments as strings, the form report
// parameters take.
func decodeArguments(raw string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("analytics: decode tool arguments: %w", err)
	}
	for k, v := range args {
		switch x := v.(type) {
		case nil:
		case string:
			out[k] = x
		case float64:
			out[k] = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			out[k] = strconv.FormatBool(x)
		default:
			return nil, fmt.Errorf("analytics: tool argument %s is not a scalar", k)
		}
	}
	return out, nil
}
