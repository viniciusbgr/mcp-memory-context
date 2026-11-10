package synchronizer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/viniciusbgr/mcp-memory-context/internal/memory"
)

func TestServiceCreatesAndIndexesMemory(t *testing.T) {
	t.Parallel()

	store := &fakeMemoryStore{readErr: fmt.Errorf("read: %w", os.ErrNotExist)}
	service := NewService(
		fakeSourceReader{content: "package billing\n\nfunc Issue() {}\n"},
		store,
		fakeLLM{response: `{"summary":"Issues invoices.","summary_evidence":{"symbol":"file","start_line":1,"end_line":1,"kind":"file"},"exports":[{"symbol":"Issue","summary":"Issues invoices.","start_line":3,"end_line":3,"kind":"declaration"}],"dependencies":[],"rules":[],"test_scenarios":[],"gaps":[]}`},
	)

	output, err := service.Sync(context.Background(), SyncInput{SourcePath: "internal/billing/issue.go"})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if output.Action != "created" || output.MemoryPath != "internal/billing/issue.go.memory" {
		t.Fatalf("Sync() output = %#v", output)
	}
	if store.writtenPath != output.MemoryPath || store.indexedPath != output.MemoryPath {
		t.Fatalf("store paths = write %q, index %q", store.writtenPath, store.indexedPath)
	}
}

func TestServiceDoesNotWriteInvalidGeneratedMemory(t *testing.T) {
	t.Parallel()

	store := &fakeMemoryStore{readErr: fmt.Errorf("read: %w", os.ErrNotExist)}
	service := NewService(
		fakeSourceReader{content: "package billing\n\nfunc Issue() {}\n"},
		store,
		&sequenceLLM{responses: []string{
			`{"summary":"Invalid export.","summary_evidence":{"symbol":"file","start_line":1,"end_line":1,"kind":"file"},"exports":[{"symbol":"Missing","summary":"Missing.","start_line":3,"end_line":3,"kind":"declaration"}],"dependencies":[],"rules":[],"test_scenarios":[],"gaps":[]}`,
			`{"summary":"Still invalid.","summary_evidence":{"symbol":"file","start_line":1,"end_line":1,"kind":"file"},"exports":[{"symbol":"Missing","summary":"Missing.","start_line":3,"end_line":3,"kind":"declaration"}],"dependencies":[],"rules":[],"test_scenarios":[],"gaps":[]}`,
		}},
	)

	_, err := service.Sync(context.Background(), SyncInput{SourcePath: "internal/billing/issue.go"})
	if err == nil || !strings.Contains(err.Error(), "unknown exported symbol") {
		t.Fatalf("Sync() error = %v, want semantic validation error", err)
	}
	if store.writtenPath != "" || store.indexedPath != "" {
		t.Fatalf("store was modified: %#v", store)
	}
}

func TestServiceUpdatesExistingMemory(t *testing.T) {
	t.Parallel()

	store := &fakeMemoryStore{document: memory.Document{
		Path:    "internal/billing/issue.go.memory",
		Content: "---\ntype: source\ngaps: []\n---\n\n# Summary\n\n- Previous summary.\n",
	}}
	service := NewService(
		fakeSourceReader{content: "package billing\n\nfunc Issue() {}\n"},
		store,
		fakeLLM{response: `{"summary":"Updated summary.","summary_evidence":{"symbol":"file","start_line":1,"end_line":1,"kind":"file"},"exports":[{"symbol":"Issue","summary":"Issues invoices.","start_line":3,"end_line":3,"kind":"declaration"}],"dependencies":[],"rules":[],"test_scenarios":[],"gaps":[]}`},
	)

	output, err := service.Sync(context.Background(), SyncInput{SourcePath: "internal/billing/issue.go"})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if output.Action != "updated" || store.writtenPath == "" || store.indexedPath == "" {
		t.Fatalf("Sync() output = %#v; store = %#v", output, store)
	}
}

func TestServiceRendersValidatedStructuredMemory(t *testing.T) {
	t.Parallel()

	store := &fakeMemoryStore{readErr: fmt.Errorf("read: %w", os.ErrNotExist)}
	service := NewService(
		fakeSourceReader{content: "package billing\n\n// Issue creates an invoice.\nfunc Issue() {}\n"},
		store,
		fakeLLM{response: "```json\n{\"summary\":\"Creates invoices.\",\"summary_evidence\":{\"symbol\":\"file\",\"start_line\":1,\"end_line\":1,\"kind\":\"file\"},\"exports\":[{\"symbol\":\"Issue\",\"summary\":\"Creates an invoice.\",\"start_line\":4,\"end_line\":4,\"kind\":\"declaration\"}],\"dependencies\":[],\"rules\":[],\"test_scenarios\":[],\"gaps\":[]}\n```"},
	)

	if _, err := service.Sync(context.Background(), SyncInput{SourcePath: "internal/billing/issue.go"}); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if store.writtenPath == "" {
		t.Fatal("Sync() did not write the validated document")
	}
	if strings.Contains(store.writtenContent, "```") || !strings.Contains(store.writtenContent, "source_hash:") {
		t.Fatalf("written memory = %q", store.writtenContent)
	}
}

func TestServiceRepairsInvalidGeneratedMemoryOnce(t *testing.T) {
	t.Parallel()

	store := &fakeMemoryStore{readErr: fmt.Errorf("read: %w", os.ErrNotExist)}
	service := NewService(
		fakeSourceReader{content: "package billing\n"},
		store,
		&sequenceLLM{responses: []string{
			`{"summary":"","summary_evidence":{"symbol":"file","start_line":1,"end_line":1,"kind":"file"},"exports":[],"dependencies":[],"rules":[],"test_scenarios":[],"gaps":[]}`,
			`{"summary":"Repaired memory.","summary_evidence":{"symbol":"file","start_line":1,"end_line":1,"kind":"file"},"exports":[],"dependencies":[],"rules":[],"test_scenarios":[],"gaps":[]}`,
		}},
	)

	if _, err := service.Sync(context.Background(), SyncInput{SourcePath: "internal/billing/issue.go"}); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if store.writtenPath == "" {
		t.Fatal("Sync() did not write the repaired document")
	}
}

func TestServiceRejectsCodeBlocksAndUndeclaredDependencies(t *testing.T) {
	t.Parallel()

	store := &fakeMemoryStore{readErr: fmt.Errorf("read: %w", os.ErrNotExist)}
	service := NewService(
		fakeSourceReader{content: "package billing\n"},
		store,
		&sequenceLLM{responses: []string{
			"{\"summary\":\"```go\\npackage billing\\n```\",\"summary_evidence\":{\"symbol\":\"file\",\"start_line\":1,\"end_line\":1,\"kind\":\"file\"},\"exports\":[],\"dependencies\":[\"net/http\"],\"rules\":[],\"test_scenarios\":[],\"gaps\":[]}",
			"{\"summary\":\"```go\\npackage billing\\n```\",\"summary_evidence\":{\"symbol\":\"file\",\"start_line\":1,\"end_line\":1,\"kind\":\"file\"},\"exports\":[],\"dependencies\":[\"net/http\"],\"rules\":[],\"test_scenarios\":[],\"gaps\":[]}",
		}},
	)

	if _, err := service.Sync(context.Background(), SyncInput{SourcePath: "internal/billing/issue.go"}); err == nil {
		t.Fatal("Sync() error = nil, want rejected unsafe content")
	}
	if store.writtenPath != "" {
		t.Fatalf("Sync() wrote invalid content to %q", store.writtenPath)
	}
}

func TestParseGeneratedDocumentRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	_, err := parseGeneratedDocument(`{"summary":"Summary.","summary_evidence":{"symbol":"file","start_line":1,"end_line":1,"kind":"file"},"exports":[],"dependencies":[],"rules":[],"test_scenarios":[],"gaps":[],"unexpected":true}`)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("parseGeneratedDocument() error = %v, want unknown-field error", err)
	}
}

func TestServiceRejectsEvidenceOutsideSourceConstruct(t *testing.T) {
	t.Parallel()

	store := &fakeMemoryStore{readErr: fmt.Errorf("read: %w", os.ErrNotExist)}
	service := NewService(
		fakeSourceReader{content: "package billing\n\nfunc Issue() string {\n\treturn \"issued\"\n}\n"},
		store,
		&sequenceLLM{responses: []string{
			`{"summary":"Issues invoices.","summary_evidence":{"symbol":"file","start_line":1,"end_line":1,"kind":"file"},"exports":[{"symbol":"Issue","summary":"Issues an invoice.","start_line":3,"end_line":5,"kind":"declaration"}],"dependencies":[],"rules":[{"symbol":"Issue","summary":"Returns an issued value.","start_line":2,"end_line":2,"kind":"return"}],"test_scenarios":[],"gaps":[]}`,
			`{"summary":"Issues invoices.","summary_evidence":{"symbol":"file","start_line":1,"end_line":1,"kind":"file"},"exports":[{"symbol":"Issue","summary":"Issues an invoice.","start_line":3,"end_line":5,"kind":"declaration"}],"dependencies":[],"rules":[{"symbol":"Issue","summary":"Returns an issued value.","start_line":2,"end_line":2,"kind":"return"}],"test_scenarios":[],"gaps":[]}`,
		}},
	)

	if _, err := service.Sync(context.Background(), SyncInput{SourcePath: "internal/billing/issue.go"}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Sync() error = %v, want invalid evidence rejection", err)
	}
}

func TestServiceRendersTestScenarioWithVerifiedAssertion(t *testing.T) {
	t.Parallel()

	store := &fakeMemoryStore{readErr: fmt.Errorf("read: %w", os.ErrNotExist)}
	service := NewService(
		fakeSourceReader{content: "package billing\n\nimport \"testing\"\n\nfunc TestIssue(t *testing.T) {\n\tt.Error(\"failed\")\n}\n"},
		store,
		fakeLLM{response: `{"summary":"Verifies issuance.","summary_evidence":{"symbol":"file","start_line":1,"end_line":1,"kind":"file"},"exports":[],"dependencies":["testing"],"rules":[],"test_scenarios":[{"symbol":"TestIssue","summary":"Reports a failed issue.","start_line":6,"end_line":6,"kind":"assertion"}],"gaps":[]}`},
	)

	if _, err := service.Sync(context.Background(), SyncInput{SourcePath: "internal/billing/issue_test.go"}); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if !strings.Contains(store.writtenContent, "`TestIssue` (L6)") {
		t.Fatalf("written memory lacks test provenance: %q", store.writtenContent)
	}
}

func TestServiceRendersRepositoryProvenance(t *testing.T) {
	t.Parallel()

	store := &fakeMemoryStore{readErr: fmt.Errorf("read: %w", os.ErrNotExist)}
	service, err := NewServiceWithOptions(
		fakeSourceReader{content: "package billing\n\nfunc Issue() {}\n"},
		store,
		fakeLLM{response: `{"summary":"Issues invoices.","summary_evidence":{"symbol":"file","start_line":1,"end_line":1,"kind":"file"},"exports":[{"symbol":"Issue","summary":"Issues invoices.","start_line":3,"end_line":3,"kind":"declaration"}],"dependencies":[],"rules":[],"test_scenarios":[],"gaps":[]}`},
		Options{ProvenanceFormat: "repository"},
	)
	if err != nil {
		t.Fatalf("NewServiceWithOptions() error = %v", err)
	}

	if _, err := service.Sync(context.Background(), SyncInput{SourcePath: "internal/billing/issue.go"}); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if !strings.Contains(store.writtenContent, "`Issue` (internal/billing/issue.go#L3)") {
		t.Fatalf("written memory lacks repository provenance: %q", store.writtenContent)
	}
}

type fakeSourceReader struct {
	content string
}

func (r fakeSourceReader) Read(context.Context, string) (string, error) {
	return r.content, nil
}

type fakeMemoryStore struct {
	document       memory.Document
	readErr        error
	writtenPath    string
	writtenContent string
	indexedPath    string
}

func (s *fakeMemoryStore) Read(context.Context, string) (memory.Document, error) {
	return s.document, s.readErr
}

func (s *fakeMemoryStore) Write(_ context.Context, path, content string) error {
	s.writtenPath = path
	s.writtenContent = content
	return nil
}

func (s *fakeMemoryStore) EnsureIndexed(_ context.Context, path string) error {
	s.indexedPath = path
	return nil
}

type fakeLLM struct {
	response string
}

func (l fakeLLM) Generate(context.Context, string) (string, error) {
	return l.response, nil
}

type sequenceLLM struct {
	responses []string
}

func (l *sequenceLLM) Generate(context.Context, string) (string, error) {
	if len(l.responses) == 0 {
		return "", errors.New("unexpected generation request")
	}
	response := l.responses[0]
	l.responses = l.responses[1:]
	return response, nil
}
