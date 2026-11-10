package explainer

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/viniciusbgr/mcp-memory-context/internal/memory"
)

func TestServiceExplainsFeatureUsingHierarchicalMemory(t *testing.T) {
	t.Parallel()

	memory := &fakeMemory{
		documents: map[string]string{
			"index.memory":                     "- memory/internal/billing/index.memory\n",
			"internal/billing/index.memory":    "- memory/internal/billing/issue.go.memory\n",
			"internal/billing/issue.go.memory": "Boleto issuance validates an invoice, creates a bank request, and persists the identifier.\n",
		},
		gaps: map[string][]string{
			"internal/billing/issue.go.memory": {"Bank retry policy is not implemented."},
		},
	}
	llm := &fakeLLM{responses: []string{
		"```json\n{\"indexes\":[\"internal/billing/index.memory\"]}\n```",
		"```\n{\"files\":[\"internal/billing/issue.go.memory\"]}\n```",
		"Boleto issuance starts after invoice validation and sends a request to the configured bank provider.",
	}}
	service := NewService(memory, llm)

	output, err := service.ExplainFeature(context.Background(), ExplainFeatureInput{
		Question: "How does boleto issuance work?",
	})
	if err != nil {
		t.Fatalf("ExplainFeature() error = %v", err)
	}

	if !strings.Contains(output.Answer, "invoice validation") {
		t.Fatalf("ExplainFeature() answer = %q", output.Answer)
	}
	wantSources := []string{
		"index.memory",
		"internal/billing/index.memory",
		"internal/billing/issue.go.memory",
	}
	if fmt.Sprint(output.Sources) != fmt.Sprint(wantSources) {
		t.Fatalf("ExplainFeature() sources = %v, want %v", output.Sources, wantSources)
	}
	if fmt.Sprint(output.Gaps) != "[Bank retry policy is not implemented.]" {
		t.Fatalf("ExplainFeature() gaps = %v", output.Gaps)
	}
}

func TestServiceRejectsLLMSelectionsNotDeclaredByIndex(t *testing.T) {
	t.Parallel()

	memory := &fakeMemory{
		documents: map[string]string{
			"index.memory": "- memory/internal/billing/index.memory\n",
		},
	}
	llm := &fakeLLM{responses: []string{
		`{"indexes":["secrets.memory"]}`,
	}}
	service := NewService(memory, llm)

	_, err := service.ExplainFeature(context.Background(), ExplainFeatureInput{
		Question: "What is the database password?",
	})
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("ExplainFeature() error = %v, want undeclared selection error", err)
	}
}

type fakeMemory struct {
	documents map[string]string
	gaps      map[string][]string
}

func (m *fakeMemory) ReadProjectIndex(context.Context) (memory.Document, error) {
	return m.Read(context.Background(), "index.memory")
}

func (m *fakeMemory) Read(_ context.Context, path string) (memory.Document, error) {
	content, ok := m.documents[path]
	if !ok {
		return memory.Document{}, fmt.Errorf("document %q not found", path)
	}
	return memory.Document{Path: path, Content: content, Gaps: m.gaps[path]}, nil
}

func (m *fakeMemory) DeclaredReferences(path string) ([]string, error) {
	document, err := m.Read(context.Background(), path)
	if err != nil {
		return nil, err
	}
	var references []string
	for _, line := range strings.Split(document.Content, "\n") {
		const prefix = "- memory/"
		if strings.HasPrefix(line, prefix) {
			references = append(references, strings.TrimPrefix(line, prefix))
		}
	}
	return references, nil
}

func (m *fakeMemory) ReadDeclared(ctx context.Context, parentPath, path string) (memory.Document, error) {
	references, err := m.DeclaredReferences(parentPath)
	if err != nil {
		return memory.Document{}, err
	}
	for _, reference := range references {
		if reference == path {
			return m.Read(ctx, path)
		}
	}
	return memory.Document{}, fmt.Errorf("%q is not declared by %q", path, parentPath)
}

type fakeLLM struct {
	responses []string
}

func (l *fakeLLM) Generate(context.Context, string) (string, error) {
	if len(l.responses) == 0 {
		return "", fmt.Errorf("unexpected generation request")
	}
	response := l.responses[0]
	l.responses = l.responses[1:]
	return response, nil
}
