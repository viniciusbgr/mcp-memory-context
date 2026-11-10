// Package memory provides validated access to project memory documents.
package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var memoryReferencePattern = regexp.MustCompile(`(?:^|[\s(\["'` + "`" + `])-?(?:memory/)?([A-Za-z0-9][A-Za-z0-9._/-]*\.memory)(?:$|[\s)\]"',])`)

type Document struct {
	Path    string
	Content string
	Gaps    []string
}

type Reader interface {
	ReadProjectIndex(ctx context.Context) (Document, error)
	Read(ctx context.Context, path string) (Document, error)
	ReadDeclared(ctx context.Context, parentPath, path string) (Document, error)
	DeclaredReferences(path string) ([]string, error)
}

type Repository struct {
	root  string
	locks sync.Map
}

func NewRepository(root string) (*Repository, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("memory root is required")
	}

	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve memory root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve memory root symlinks: %w", err)
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil {
		return nil, fmt.Errorf("stat memory root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("memory root %q is not a directory", root)
	}

	return &Repository{root: resolvedRoot}, nil
}

func (r *Repository) ReadProjectIndex(ctx context.Context) (Document, error) {
	return r.Read(ctx, "index.memory")
}

func (r *Repository) Read(ctx context.Context, path string) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}

	normalized, err := r.normalizePath(path)
	if err != nil {
		return Document{}, err
	}
	fullPath := filepath.Join(r.root, filepath.FromSlash(normalized))
	resolvedPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return Document{}, fmt.Errorf("resolve memory document %q: %w", normalized, err)
	}
	if !isWithinRoot(r.root, resolvedPath) {
		return Document{}, fmt.Errorf("memory document %q resolves outside the memory root", normalized)
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return Document{}, fmt.Errorf("read memory document %q: %w", normalized, err)
	}
	gaps, err := parseDeclaredGaps(string(content))
	if err != nil {
		return Document{}, fmt.Errorf("parse gaps in memory document %q: %w", normalized, err)
	}
	return Document{Path: normalized, Content: string(content), Gaps: gaps}, nil
}

func (r *Repository) Write(ctx context.Context, path, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	normalized, err := r.normalizePath(path)
	if err != nil {
		return err
	}
	unlock := r.lock(normalized)
	defer unlock()
	return r.write(ctx, normalized, content)
}

func (r *Repository) write(ctx context.Context, normalized, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fullPath := filepath.Join(r.root, filepath.FromSlash(normalized))
	parent := filepath.Dir(fullPath)
	resolvedExistingAncestor, err := resolveExistingAncestor(parent)
	if err != nil {
		return fmt.Errorf("resolve existing memory directory for %q: %w", normalized, err)
	}
	if !isWithinRoot(r.root, resolvedExistingAncestor) {
		return fmt.Errorf("memory directory for %q resolves outside the memory root", normalized)
	}
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return fmt.Errorf("create memory directory for %q: %w", normalized, err)
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("resolve memory directory for %q: %w", normalized, err)
	}
	if !isWithinRoot(r.root, resolvedParent) {
		return fmt.Errorf("memory directory for %q resolves outside the memory root", normalized)
	}

	target := filepath.Join(resolvedParent, filepath.Base(normalized))
	if err := writeFileAtomically(target, []byte(content)); err != nil {
		return fmt.Errorf("write memory document %q: %w", normalized, err)
	}
	return nil
}

func (r *Repository) EnsureIndexed(ctx context.Context, path string) error {
	normalized, err := r.normalizePath(path)
	if err != nil {
		return err
	}

	directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(normalized)))
	if directory == "." {
		return r.ensureIndexReference(ctx, "index.memory", normalized)
	}

	packageIndex := directory + "/index.memory"
	if err := r.ensureIndexReference(ctx, packageIndex, normalized); err != nil {
		return err
	}
	return r.ensureIndexReference(ctx, "index.memory", packageIndex)
}

func (r *Repository) ReadDeclared(ctx context.Context, parentPath, path string) (Document, error) {
	references, err := r.DeclaredReferences(parentPath)
	if err != nil {
		return Document{}, err
	}
	normalized, err := r.normalizePath(path)
	if err != nil {
		return Document{}, err
	}
	for _, reference := range references {
		if reference == normalized {
			return r.Read(ctx, normalized)
		}
	}
	return Document{}, fmt.Errorf("memory document %q is not declared by %q", normalized, parentPath)
}

func (r *Repository) DeclaredReferences(path string) ([]string, error) {
	document, err := r.Read(context.Background(), path)
	if err != nil {
		return nil, err
	}

	matches := memoryReferencePattern.FindAllStringSubmatch(document.Content, -1)
	references := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		normalized, err := r.normalizePath(match[1])
		if err != nil {
			return nil, fmt.Errorf("invalid reference in %q: %w", document.Path, err)
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		references = append(references, normalized)
	}
	return references, nil
}

func (r *Repository) normalizePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("memory path is required")
	}
	path = strings.TrimPrefix(filepath.ToSlash(path), "memory/")
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if filepath.IsAbs(path) || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("invalid memory path %q", path)
	}
	if !strings.HasSuffix(cleaned, ".memory") {
		return "", fmt.Errorf("memory path %q must end in .memory", path)
	}
	return cleaned, nil
}

func (r *Repository) ensureIndexReference(ctx context.Context, indexPath, reference string) error {
	unlock := r.lock(indexPath)
	defer unlock()
	index, err := r.Read(ctx, indexPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		index = Document{
			Path:    indexPath,
			Content: "---\ntype: index\ngaps: []\n---\n\n# Memory Index\n",
		}
	}

	references, err := r.DeclaredReferences(index.Path)
	if err == nil {
		for _, declared := range references {
			if declared == reference {
				return nil
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	content := strings.TrimRight(index.Content, "\n") + "\n\n- memory/" + reference + "\n"
	return r.write(ctx, index.Path, content)
}

func (r *Repository) ListDocuments(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var paths []string
	err := filepath.WalkDir(r.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".memory") {
			return nil
		}
		relative, err := filepath.Rel(r.root, path)
		if err != nil {
			return err
		}
		normalized, err := r.normalizePath(relative)
		if err != nil {
			return err
		}
		paths = append(paths, normalized)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list memory documents: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}

func (r *Repository) ReconcileIndexes(ctx context.Context, documentPaths []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	grouped := make(map[string][]string)
	for _, path := range documentPaths {
		normalized, err := r.normalizePath(path)
		if err != nil {
			return err
		}
		directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(normalized)))
		grouped[directory] = append(grouped[directory], normalized)
	}

	directories := make([]string, 0, len(grouped))
	for directory := range grouped {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	for _, directory := range directories {
		if directory == "." {
			continue
		}
		references := append([]string(nil), grouped[directory]...)
		sort.Strings(references)
		if err := r.replaceIndex(ctx, directory+"/index.memory", references); err != nil {
			return err
		}
	}
	rootReferences := append([]string(nil), grouped["."]...)
	for _, directory := range directories {
		if directory != "." {
			rootReferences = append(rootReferences, directory+"/index.memory")
		}
	}
	sort.Strings(rootReferences)
	return r.replaceIndex(ctx, "index.memory", rootReferences)
}

func (r *Repository) replaceIndex(ctx context.Context, indexPath string, references []string) error {
	unlock := r.lock(indexPath)
	defer unlock()
	var content strings.Builder
	content.WriteString("---\ntype: index\ngaps: []\n---\n\n# Memory Index\n")
	for _, reference := range references {
		fmt.Fprintf(&content, "\n- memory/%s\n", reference)
	}
	return r.write(ctx, indexPath, content.String())
}

func (r *Repository) lock(path string) func() {
	value, _ := r.locks.LoadOrStore(path, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func ValidateDocument(content, expectedType string) error {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return errors.New("memory document must start with YAML frontmatter")
	}

	frontmatterEnd := -1
	documentType := ""
	for index := 1; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "---" {
			frontmatterEnd = index
			break
		}
		if strings.HasPrefix(line, "type:") {
			documentType = strings.TrimSpace(strings.TrimPrefix(line, "type:"))
		}
	}
	if frontmatterEnd == -1 {
		return errors.New("memory document frontmatter is not closed")
	}
	if documentType == "" {
		return errors.New("memory document type is required")
	}
	if expectedType != "" && documentType != expectedType {
		return fmt.Errorf("memory document type %q does not match expected type %q", documentType, expectedType)
	}
	if _, err := parseDeclaredGaps(content); err != nil {
		return err
	}
	if strings.TrimSpace(strings.Join(lines[frontmatterEnd+1:], "\n")) == "" {
		return errors.New("memory document body is required")
	}
	return nil
}

func writeFileAtomically(path string, content []byte) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tempPath)
		}
	}()

	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func resolveExistingAncestor(path string) (string, error) {
	for {
		resolvedPath, err := filepath.EvalSymlinks(path)
		if err == nil {
			return resolvedPath, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		path = parent
	}
}

func isWithinRoot(root, path string) bool {
	relativePath, err := filepath.Rel(root, path)
	return err == nil && relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator))
}

func parseDeclaredGaps(content string) ([]string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, nil
	}

	frontmatterEnd := -1
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			frontmatterEnd = index
			break
		}
	}
	if frontmatterEnd == -1 {
		return nil, errors.New("frontmatter is not closed")
	}

	for index := 1; index < frontmatterEnd; index++ {
		line := strings.TrimSpace(lines[index])
		switch line {
		case "gaps: []":
			return nil, nil
		case "gaps:":
			var gaps []string
			for index++; index < frontmatterEnd; index++ {
				item := strings.TrimSpace(lines[index])
				if item == "" {
					continue
				}
				if !strings.HasPrefix(item, "- ") {
					break
				}
				gap := strings.TrimSpace(strings.TrimPrefix(item, "- "))
				if gap == "" {
					return nil, errors.New("gap entries must not be empty")
				}
				gaps = append(gaps, gap)
			}
			return gaps, nil
		default:
			if strings.HasPrefix(line, "gaps:") {
				return nil, errors.New("gaps must be a YAML list or []")
			}
		}
	}
	return nil, nil
}
