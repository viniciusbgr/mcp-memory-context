// Package mcp implements the JSON-RPC surface for the memory explanation tool.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/viniciusbgr/mcp-memory-context/internal/explainer"
	"github.com/viniciusbgr/mcp-memory-context/internal/synchronizer"
)

type FeatureExplainer interface {
	ExplainFeature(ctx context.Context, input explainer.ExplainFeatureInput) (explainer.ExplainFeatureOutput, error)
}

type MemorySynchronizer interface {
	Sync(ctx context.Context, input synchronizer.SyncInput) (synchronizer.SyncOutput, error)
}

type Server struct {
	explainer    FeatureExplainer
	synchronizer MemorySynchronizer
}

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type CallToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func NewServer(featureExplainer FeatureExplainer) *Server {
	return &Server{explainer: featureExplainer}
}

func NewServerWithSynchronizer(featureExplainer FeatureExplainer, memorySynchronizer MemorySynchronizer) *Server {
	return &Server{explainer: featureExplainer, synchronizer: memorySynchronizer}
}

func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		var request Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if err := encoder.Encode(errorResponse(nil, -32700, "invalid JSON-RPC request")); err != nil {
				return fmt.Errorf("write parse error response: %w", err)
			}
			continue
		}
		if err := encoder.Encode(s.Handle(ctx, request)); err != nil {
			return fmt.Errorf("write JSON-RPC response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read JSON-RPC request: %w", err)
	}
	return nil
}

func (s *Server) Handle(ctx context.Context, request Request) Response {
	if request.JSONRPC != "2.0" {
		return errorResponse(request.ID, -32600, "jsonrpc must be 2.0")
	}
	switch request.Method {
	case "initialize":
		return successResponse(request.ID, map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "mcp-memory-context", "version": "0.1.0"},
		})
	case "notifications/initialized":
		return successResponse(request.ID, map[string]any{})
	case "tools/list":
		tools := []Tool{explainFeatureTool()}
		if s.synchronizer != nil {
			tools = append(tools, syncMemoryTool())
		}
		return successResponse(request.ID, map[string]any{"tools": tools})
	case "tools/call":
		return s.callTool(ctx, request)
	default:
		return errorResponse(request.ID, -32601, "method not found")
	}
}

func (s *Server) callTool(ctx context.Context, request Request) Response {
	var parameters struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(request.Params, &parameters); err != nil {
		return errorResponse(request.ID, -32602, "invalid tool call parameters")
	}
	switch parameters.Name {
	case "explain_feature":
		return s.callExplainFeature(ctx, request.ID, parameters.Arguments)
	case "sync_memory":
		return s.callSyncMemory(ctx, request.ID, parameters.Arguments)
	default:
		return errorResponse(request.ID, -32602, "unknown tool")
	}
}

func (s *Server) callExplainFeature(ctx context.Context, id, arguments json.RawMessage) Response {
	if s.explainer == nil {
		return errorResponse(id, -32603, "feature explainer is not configured")
	}
	var input explainer.ExplainFeatureInput
	if err := json.Unmarshal(arguments, &input); err != nil {
		return errorResponse(id, -32602, "invalid explain_feature arguments")
	}
	if strings.TrimSpace(input.Question) == "" {
		return errorResponse(id, -32602, "question is required")
	}
	output, err := s.explainer.ExplainFeature(ctx, input)
	if err != nil {
		return successResponse(id, CallToolResult{
			Content: []Content{{Type: "text", Text: "Unable to explain the feature: " + err.Error()}},
			IsError: true,
		})
	}
	text, err := json.Marshal(output)
	if err != nil {
		return errorResponse(id, -32603, "encode explanation")
	}
	return successResponse(id, CallToolResult{
		Content: []Content{{Type: "text", Text: string(text)}},
	})
}

func (s *Server) callSyncMemory(ctx context.Context, id, arguments json.RawMessage) Response {
	if s.synchronizer == nil {
		return errorResponse(id, -32603, "memory synchronizer is not configured")
	}
	var input synchronizer.SyncInput
	if err := json.Unmarshal(arguments, &input); err != nil {
		return errorResponse(id, -32602, "invalid sync_memory arguments")
	}
	if strings.TrimSpace(input.SourcePath) == "" {
		return errorResponse(id, -32602, "source_path is required")
	}
	output, err := s.synchronizer.Sync(ctx, input)
	if err != nil {
		return successResponse(id, CallToolResult{
			Content: []Content{{Type: "text", Text: "Unable to synchronize memory: " + err.Error()}},
			IsError: true,
		})
	}
	text, err := json.Marshal(output)
	if err != nil {
		return errorResponse(id, -32603, "encode memory synchronization")
	}
	return successResponse(id, CallToolResult{
		Content: []Content{{Type: "text", Text: string(text)}},
	})
}

func explainFeatureTool() Tool {
	return Tool{
		Name:        "explain_feature",
		Description: "Explains how a project feature works using hierarchical local memory.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"question"},
			"properties": map[string]any{
				"question": map[string]string{
					"type":        "string",
					"description": "A natural-language question about a project feature.",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Optional maximum context size for a future configured retriever.",
					"minimum":     1,
				},
			},
		},
	}
}

func syncMemoryTool() Tool {
	return Tool{
		Name:        "sync_memory",
		Description: "Creates or updates the companion memory document for a Go source or test file.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"source_path"},
			"properties": map[string]any{
				"source_path": map[string]string{
					"type":        "string",
					"description": "Relative path to a Go source or test file under the configured project root.",
				},
			},
		},
	}
}

func successResponse(id json.RawMessage, result any) Response {
	return Response{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResponse(id json.RawMessage, code int, message string) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &ResponseError{Code: code, Message: message},
	}
}
