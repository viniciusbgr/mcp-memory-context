package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryReadsOnlyDeclaredMemory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeMemoryFile(t, root, "index.memory", "- memory/internal/billing/index.memory\n")
	writeMemoryFile(t, root, "internal/billing/index.memory", "- memory/internal/billing/issue.go.memory\n")
	writeMemoryFile(t, root, "internal/billing/issue.go.memory", "---\ngaps:\n  - Bank retry policy is not implemented.\n---\n# Issue boleto\n")
	writeMemoryFile(t, root, "secrets.memory", "private\n")

	repository, err := NewRepository(root)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}

	index, err := repository.ReadProjectIndex(context.Background())
	if err != nil {
		t.Fatalf("ReadProjectIndex() error = %v", err)
	}
	if !strings.Contains(index.Content, "billing") {
		t.Fatalf("ReadProjectIndex() content = %q", index.Content)
	}

	children, err := repository.DeclaredReferences(index.Path)
	if err != nil {
		t.Fatalf("DeclaredReferences() error = %v", err)
	}
	if len(children) != 1 || children[0] != "internal/billing/index.memory" {
		t.Fatalf("DeclaredReferences() = %v, want billing index", children)
	}

	document, err := repository.ReadDeclared(context.Background(), index.Path, children[0])
	if err != nil {
		t.Fatalf("ReadDeclared() error = %v", err)
	}
	if document.Path != children[0] || !strings.Contains(document.Content, "issue.go.memory") {
		t.Fatalf("ReadDeclared() = %#v", document)
	}

	files, err := repository.DeclaredReferences(document.Path)
	if err != nil {
		t.Fatalf("DeclaredReferences() error = %v", err)
	}
	issue, err := repository.ReadDeclared(context.Background(), document.Path, files[0])
	if err != nil {
		t.Fatalf("ReadDeclared() error = %v", err)
	}
	if !strings.Contains(issue.Content, "Issue") {
		t.Fatalf("ReadDeclared() = %#v", issue)
	}
	if len(issue.Gaps) != 1 || issue.Gaps[0] != "Bank retry policy is not implemented." {
		t.Fatalf("ReadDeclared() gaps = %v", issue.Gaps)
	}

	if _, err := repository.ReadDeclared(context.Background(), index.Path, "secrets.memory"); err == nil {
		t.Fatal("ReadDeclared() error = nil, want rejected undeclared file")
	}
}

func TestRepositoryRejectsPathsOutsideMemoryRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeMemoryFile(t, root, "index.memory", "")
	repository, err := NewRepository(root)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}

	if _, err := repository.Read(context.Background(), "../outside.memory"); err == nil {
		t.Fatal("Read() error = nil, want path traversal rejection")
	}
}

func writeMemoryFile(t *testing.T, root, path, content string) {
	t.Helper()

	fullPath := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o750); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", fullPath, err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", fullPath, err)
	}
}
