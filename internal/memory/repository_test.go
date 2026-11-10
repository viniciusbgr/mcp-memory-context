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

func TestRepositoryWritesAndIndexesMemory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeMemoryFile(t, root, "index.memory", "---\ntype: index\ngaps: []\n---\n\n# Project Memory Index\n")
	repository, err := NewRepository(root)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}

	content := "---\ntype: source\ngaps: []\n---\n\n# Summary\n\n- Synchronizes memory.\n"
	if err := repository.Write(context.Background(), "internal/synchronizer/service.go.memory", content); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := repository.EnsureIndexed(context.Background(), "internal/synchronizer/service.go.memory"); err != nil {
		t.Fatalf("EnsureIndexed() error = %v", err)
	}
	if err := repository.EnsureIndexed(context.Background(), "internal/synchronizer/service.go.memory"); err != nil {
		t.Fatalf("EnsureIndexed() repeat error = %v", err)
	}

	document, err := repository.Read(context.Background(), "internal/synchronizer/service.go.memory")
	if err != nil || document.Content != content {
		t.Fatalf("Read() = %#v, %v", document, err)
	}
	packageIndex, err := repository.Read(context.Background(), "internal/synchronizer/index.memory")
	if err != nil || strings.Count(packageIndex.Content, "service.go.memory") != 1 {
		t.Fatalf("package index = %#v, %v", packageIndex, err)
	}
	projectIndex, err := repository.ReadProjectIndex(context.Background())
	if err != nil || strings.Count(projectIndex.Content, "internal/synchronizer/index.memory") != 1 {
		t.Fatalf("project index = %#v, %v", projectIndex, err)
	}
}

func TestValidateDocumentRejectsInvalidFrontmatter(t *testing.T) {
	t.Parallel()

	if err := ValidateDocument("---\ntype: test\ngaps: []\n---\n\n# Summary\n", "source"); err == nil {
		t.Fatal("ValidateDocument() error = nil, want type mismatch")
	}
}

func TestRepositoryDoesNotCreateDirectoriesThroughExternalSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	repository, err := NewRepository(root)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}

	err = repository.Write(context.Background(), "escape/nested/document.memory", "content")
	if err == nil || !strings.Contains(err.Error(), "outside the memory root") {
		t.Fatalf("Write() error = %v, want outside-root rejection", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "nested")); !os.IsNotExist(err) {
		t.Fatalf("external nested directory exists or stat failed: %v", err)
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
