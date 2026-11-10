package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/viniciusbgr/mcp-memory-context/internal/explainer"
	"github.com/viniciusbgr/mcp-memory-context/internal/memory"
	"github.com/viniciusbgr/mcp-memory-context/internal/ollama"
	"github.com/viniciusbgr/mcp-memory-context/internal/synchronizer"
)

func TestServerExplainsOllamaClientThroughMCP(t *testing.T) {
	if os.Getenv("OLLAMA_INTEGRATION") != "1" {
		t.Skip("set OLLAMA_INTEGRATION=1 and OLLAMA_MODEL to run the local MCP integration check")
	}

	model := strings.TrimSpace(os.Getenv("OLLAMA_MODEL"))
	if model == "" {
		t.Fatal("OLLAMA_MODEL must be set for the local MCP integration check")
	}

	repository, err := memory.NewRepository(filepath.Join("..", "..", "memory"))
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	llm, err := ollama.NewClient(ollama.Config{
		BaseURL: "http://127.0.0.1:11434",
		Model:   model,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	server := NewServer(explainer.NewService(repository, llm))

	request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"explain_feature","arguments":{"question":"How does the Ollama client work in this project?"}}}`
	var response bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := server.Serve(ctx, strings.NewReader(request+"\n"), &response); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var result Response
	if err := json.Unmarshal(response.Bytes(), &result); err != nil {
		t.Fatalf("decode MCP response: %v; response=%s", err, response.String())
	}
	if result.Error != nil {
		t.Fatalf("MCP response error: %#v", result.Error)
	}

	rawResult, err := json.Marshal(result.Result)
	if err != nil {
		t.Fatalf("encode MCP result: %v", err)
	}
	var toolResult CallToolResult
	if err := json.Unmarshal(rawResult, &toolResult); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if toolResult.IsError {
		t.Fatalf("explain_feature error: %s", toolResult.Content[0].Text)
	}
	if len(toolResult.Content) != 1 {
		t.Fatalf("tool content count = %d, want 1", len(toolResult.Content))
	}

	var explanation explainer.ExplainFeatureOutput
	if err := json.Unmarshal([]byte(toolResult.Content[0].Text), &explanation); err != nil {
		t.Fatalf("decode explanation: %v; content=%s", err, toolResult.Content[0].Text)
	}
	if strings.TrimSpace(explanation.Answer) == "" {
		t.Fatal("explanation answer is empty")
	}
	if !contains(explanation.Sources, "internal/ollama/client.go.memory") {
		t.Fatalf("explanation sources = %v, want the Ollama client memory", explanation.Sources)
	}
}

func TestServerSynchronizesMemoryThroughMCP(t *testing.T) {
	if os.Getenv("OLLAMA_INTEGRATION") != "1" {
		t.Skip("set OLLAMA_INTEGRATION=1 and OLLAMA_MODEL to run the local MCP integration check")
	}

	model := strings.TrimSpace(os.Getenv("OLLAMA_MODEL"))
	if model == "" {
		t.Fatal("OLLAMA_MODEL must be set for the local MCP integration check")
	}

	projectRoot := t.TempDir()
	sourcePath := filepath.Join(projectRoot, "internal", "demo", "greeting.go")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o750); err != nil {
		t.Fatalf("MkdirAll source directory: %v", err)
	}
	source := "package demo\n\nimport \"strings\"\n\n// Greeting returns a normalized greeting.\nfunc Greeting(name string) string {\n\treturn \"Hello, \" + strings.TrimSpace(name)\n}\n"
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	memoryRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(memoryRoot, "index.memory"), []byte("---\ntype: index\ngaps: []\n---\n\n# Project Memory Index\n"), 0o600); err != nil {
		t.Fatalf("WriteFile memory index: %v", err)
	}
	repository, err := memory.NewRepository(memoryRoot)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	sourceRepository, err := synchronizer.NewSourceRepository(projectRoot)
	if err != nil {
		t.Fatalf("NewSourceRepository: %v", err)
	}
	llm, err := ollama.NewClient(ollama.Config{
		BaseURL: "http://127.0.0.1:11434",
		Model:   model,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	server := NewServerWithSynchronizer(
		explainer.NewService(repository, llm),
		synchronizer.NewService(sourceRepository, repository, llm),
	)

	request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sync_memory","arguments":{"source_path":"internal/demo/greeting.go"}}}`
	var response bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := server.Serve(ctx, strings.NewReader(request+"\n"), &response); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var result Response
	if err := json.Unmarshal(response.Bytes(), &result); err != nil {
		t.Fatalf("decode MCP response: %v; response=%s", err, response.String())
	}
	rawResult, err := json.Marshal(result.Result)
	if err != nil {
		t.Fatalf("encode MCP result: %v", err)
	}
	var toolResult CallToolResult
	if err := json.Unmarshal(rawResult, &toolResult); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if toolResult.IsError {
		t.Fatalf("sync_memory error: %s", toolResult.Content[0].Text)
	}

	document, err := repository.Read(context.Background(), "internal/demo/greeting.go.memory")
	if err != nil {
		t.Fatalf("Read generated memory: %v", err)
	}
	if !strings.Contains(document.Content, "schema_version: 1") || !strings.Contains(document.Content, "`Greeting` (L5-L8)") || !strings.Contains(document.Content, "`strings`") {
		t.Fatalf("generated memory lacks verified facts: %s", document.Content)
	}
	if strings.Contains(document.Content, "```") {
		t.Fatalf("generated memory contains a code block: %s", document.Content)
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
