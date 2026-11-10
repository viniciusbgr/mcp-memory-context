// Package memory provides validated access to project memory documents.
package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	root string
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
