package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/viniciusbgr/mcp-memory-context/internal/explainer"
	"github.com/viniciusbgr/mcp-memory-context/internal/synchronizer"
)

func TestServerListsExplainFeatureTool(t *testing.T) {
	t.Parallel()

	server := NewServer(fakeExplainer{})
	response := server.Handle(context.Background(), Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "tools/list",
	})

	if response.Error != nil {
		t.Fatalf("Handle() error = %#v", response.Error)
	}
	result, ok := response.Result.(map[string]any)
	if !ok {
		t.Fatalf("Handle() result = %#v", response.Result)
	}
	tools, ok := result["tools"].([]Tool)
	if !ok || len(tools) != 1 || tools[0].Name != "explain_feature" {
		t.Fatalf("Handle() tools = %#v", result["tools"])
	}
}

func TestServerCallsExplainFeatureTool(t *testing.T) {
	t.Parallel()

	server := NewServer(fakeExplainer{})
	response := server.Handle(context.Background(), Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"explain_feature","arguments":{"question":"How does boleto issuance work?"}}`),
	})

	if response.Error != nil {
		t.Fatalf("Handle() error = %#v", response.Error)
	}
	result, ok := response.Result.(CallToolResult)
	if !ok {
		t.Fatalf("Handle() result = %#v", response.Result)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("Handle() result = %#v", result)
	}
}

func TestServerCallsSyncMemoryTool(t *testing.T) {
	t.Parallel()

	server := NewServerWithSynchronizer(fakeExplainer{}, fakeSynchronizer{})
	response := server.Handle(context.Background(), Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"sync_memory","arguments":{"source_path":"internal/billing/issue.go"}}`),
	})

	if response.Error != nil {
		t.Fatalf("Handle() error = %#v", response.Error)
	}
	result, ok := response.Result.(CallToolResult)
	if !ok || result.IsError || len(result.Content) != 1 {
		t.Fatalf("Handle() result = %#v", response.Result)
	}
	if result.Content[0].Text != `{"memory_path":"internal/billing/issue.go.memory","action":"created"}` {
		t.Fatalf("Handle() content = %q", result.Content[0].Text)
	}
}

func TestServerListsSyncMemoryToolWhenConfigured(t *testing.T) {
	t.Parallel()

	server := NewServerWithSynchronizer(fakeExplainer{}, fakeSynchronizer{})
	response := server.Handle(context.Background(), Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "tools/list",
	})

	result, ok := response.Result.(map[string]any)
	if !ok {
		t.Fatalf("Handle() result = %#v", response.Result)
	}
	tools, ok := result["tools"].([]Tool)
	if !ok || len(tools) != 2 || tools[1].Name != "sync_memory" {
		t.Fatalf("Handle() tools = %#v", result["tools"])
	}
}

type fakeExplainer struct{}

func (fakeExplainer) ExplainFeature(context.Context, explainer.ExplainFeatureInput) (explainer.ExplainFeatureOutput, error) {
	return explainer.ExplainFeatureOutput{
		Answer:  "Boleto issuance validates an invoice before requesting the bank provider.",
		Sources: []string{"index.memory", "internal/billing/issue.go.memory"},
	}, nil
}

type fakeSynchronizer struct{}

func (fakeSynchronizer) Sync(context.Context, synchronizer.SyncInput) (synchronizer.SyncOutput, error) {
	return synchronizer.SyncOutput{
		MemoryPath: "internal/billing/issue.go.memory",
		Action:     "created",
	}, nil
}
