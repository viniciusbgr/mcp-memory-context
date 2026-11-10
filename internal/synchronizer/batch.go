package synchronizer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/viniciusbgr/mcp-memory-context/internal/memory"
)

type DiscoverOptions struct {
	ExcludeHidden    bool
	ExcludeGenerated bool
	ExcludeVendor    bool
	Exclude          []string
}

type RunOptions struct {
	Full    bool
	Status  bool
	Fix     bool
	Workers int
	DiscoverOptions
}

type Report struct {
	Created   []string  `json:"created"`
	Updated   []string  `json:"updated"`
	Unchanged []string  `json:"unchanged"`
	Missing   []string  `json:"missing"`
	Stale     []string  `json:"stale"`
	Invalid   []string  `json:"invalid"`
	Failed    []Failure `json:"failed"`
	Orphaned  []string  `json:"orphaned"`
}

type Failure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type Manager struct {
	source  *SourceRepository
	memory  *memory.Repository
	service *Service
}

func NewManager(source *SourceRepository, store *memory.Repository, service *Service) *Manager {
	return &Manager{source: source, memory: store, service: service}
}

func (r *SourceRepository) Discover(ctx context.Context, options DiscoverOptions) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !options.ExcludeHidden {
		options.ExcludeHidden = true
	}
	if !options.ExcludeGenerated {
		options.ExcludeGenerated = true
	}
	if !options.ExcludeVendor {
		options.ExcludeVendor = true
	}
	ignored, err := readIgnorePatterns(r.root)
	if err != nil {
		return nil, err
	}
	var paths []string
	err = filepath.WalkDir(r.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(r.root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if shouldExcludeDirectory(relative, options) || matchesIgnore(relative+"/", ignored) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(relative, ".go") || matchesIgnore(relative, ignored) || matchesExcluded(relative, options.Exclude) {
			return nil
		}
		if options.ExcludeGenerated && isGenerated(path) {
			return nil
		}
		paths = append(paths, relative)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover Go source files: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}

func (m *Manager) Run(ctx context.Context, options RunOptions) (Report, error) {
	if m == nil || m.source == nil || m.memory == nil {
		return Report{}, errors.New("synchronization manager is not configured")
	}
	paths, err := m.source.Discover(ctx, options.DiscoverOptions)
	if err != nil {
		return Report{}, err
	}
	report := Report{}
	expected := make(map[string]struct{}, len(paths))
	var pending []string
	for _, sourcePath := range paths {
		memoryPath := sourcePath + ".memory"
		expected[memoryPath] = struct{}{}
		state, err := m.memoryState(ctx, sourcePath, memoryPath)
		if err != nil {
			report.Failed = append(report.Failed, Failure{Path: memoryPath, Error: err.Error()})
			continue
		}
		switch state {
		case "missing":
			report.Missing = append(report.Missing, memoryPath)
		case "stale":
			report.Stale = append(report.Stale, memoryPath)
		case "invalid":
			report.Invalid = append(report.Invalid, memoryPath)
		case "unchanged":
			report.Unchanged = append(report.Unchanged, memoryPath)
		}
		if !options.Status && (options.Full || state != "unchanged") {
			pending = append(pending, sourcePath)
		}
	}

	orphaned, err := m.findOrphans(ctx, expected)
	if err != nil {
		report.Failed = append(report.Failed, Failure{Path: "memory", Error: err.Error()})
	} else {
		report.Orphaned = orphaned
	}
	if !options.Status {
		m.runPending(ctx, pending, options.Workers, &report)
	}
	if (options.Full || options.Fix) && len(report.Failed) == 0 {
		documents, err := m.memory.ListDocuments(ctx)
		if err != nil {
			report.Failed = append(report.Failed, Failure{Path: "memory indexes", Error: err.Error()})
			report.sort()
			return report, errors.New("memory synchronization completed with failures")
		}
		for _, path := range documents {
			if path != "index.memory" && !strings.HasSuffix(path, "/index.memory") && !strings.HasSuffix(strings.TrimSuffix(path, ".memory"), ".go") {
				expected[path] = struct{}{}
			}
		}
		expectedPaths := make([]string, 0, len(expected))
		for path := range expected {
			expectedPaths = append(expectedPaths, path)
		}
		if err := m.memory.ReconcileIndexes(ctx, expectedPaths); err != nil {
			report.Failed = append(report.Failed, Failure{Path: "memory indexes", Error: err.Error()})
		}
	}
	report.sort()
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if len(report.Failed) > 0 {
		return report, errors.New("memory synchronization completed with failures")
	}
	return report, nil
}

func (m *Manager) memoryState(ctx context.Context, sourcePath, memoryPath string) (string, error) {
	document, err := m.memory.Read(ctx, memoryPath)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	expectedType := "source"
	if strings.HasSuffix(sourcePath, "_test.go") {
		expectedType = "test"
	}
	if err := memory.ValidateDocument(document.Content, expectedType); err != nil {
		return "invalid", nil
	}
	hash, ok := sourceHash(document.Content)
	if !ok {
		return "invalid", nil
	}
	source, err := m.source.Read(ctx, sourcePath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(source))
	if hash != hex.EncodeToString(sum[:]) {
		return "stale", nil
	}
	return "unchanged", nil
}

func (m *Manager) runPending(ctx context.Context, paths []string, workers int, report *Report) {
	if len(paths) == 0 {
		return
	}
	if workers < 1 {
		workers = 1
	}
	if workers > len(paths) {
		workers = len(paths)
	}
	jobs := make(chan string)
	var wait sync.WaitGroup
	var reportMu sync.Mutex
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for path := range jobs {
				output, err := m.service.Sync(ctx, SyncInput{SourcePath: path})
				reportMu.Lock()
				if err != nil {
					report.Failed = append(report.Failed, Failure{Path: path + ".memory", Error: err.Error()})
				} else if output.Action == "created" {
					report.Created = append(report.Created, output.MemoryPath)
				} else {
					report.Updated = append(report.Updated, output.MemoryPath)
				}
				reportMu.Unlock()
			}
		}()
	}
	for _, path := range paths {
		if ctx.Err() != nil {
			break
		}
		jobs <- path
	}
	close(jobs)
	wait.Wait()
}

func (m *Manager) findOrphans(ctx context.Context, expected map[string]struct{}) ([]string, error) {
	documents, err := m.memory.ListDocuments(ctx)
	if err != nil {
		return nil, err
	}
	var orphaned []string
	for _, path := range documents {
		if path == "index.memory" || strings.HasSuffix(path, "/index.memory") {
			continue
		}
		if !strings.HasSuffix(strings.TrimSuffix(path, ".memory"), ".go") {
			continue
		}
		if _, ok := expected[path]; !ok {
			orphaned = append(orphaned, path)
		}
	}
	sort.Strings(orphaned)
	return orphaned, nil
}

func (r *Report) sort() {
	sort.Strings(r.Created)
	sort.Strings(r.Updated)
	sort.Strings(r.Unchanged)
	sort.Strings(r.Missing)
	sort.Strings(r.Stale)
	sort.Strings(r.Invalid)
	sort.Strings(r.Orphaned)
	sort.Slice(r.Failed, func(i, j int) bool { return r.Failed[i].Path < r.Failed[j].Path })
}

func sourceHash(content string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "source_hash:"); ok {
			value = strings.TrimSpace(value)
			return value, len(value) == sha256.Size*2
		}
	}
	return "", false
}

func shouldExcludeDirectory(path string, options DiscoverOptions) bool {
	for _, part := range strings.Split(path, "/") {
		if options.ExcludeHidden && strings.HasPrefix(part, ".") {
			return true
		}
		if options.ExcludeVendor && part == "vendor" {
			return true
		}
	}
	if path == "memory" || strings.HasPrefix(path, "memory/") {
		return true
	}
	return matchesExcluded(path, options.Exclude)
}

func isGenerated(path string) bool {
	if strings.HasSuffix(path, ".gen.go") || strings.HasSuffix(path, "_generated.go") {
		return true
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "// Code generated") {
			return true
		}
	}
	return false
}

func matchesExcluded(path string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.Trim(strings.TrimSpace(filepath.ToSlash(pattern)), "/")
		if pattern == "" {
			continue
		}
		if matched, _ := filepath.Match(pattern, path); matched || path == pattern || strings.HasPrefix(path, pattern+"/") {
			return true
		}
	}
	return false
}

func readIgnorePatterns(root string) ([]string, error) {
	content, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read .gitignore: %w", err)
	}
	var patterns []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "!") {
			patterns = append(patterns, filepath.ToSlash(line))
		}
	}
	return patterns, nil
}

func matchesIgnore(path string, patterns []string) bool {
	path = strings.TrimPrefix(path, "/")
	for _, pattern := range patterns {
		pattern = strings.TrimPrefix(pattern, "/")
		directoryOnly := strings.HasSuffix(pattern, "/")
		pattern = strings.TrimSuffix(pattern, "/")
		if directoryOnly && (path == pattern || strings.HasPrefix(path, pattern+"/")) {
			return true
		}
		if matched, _ := filepath.Match(pattern, path); matched {
			return true
		}
		if !strings.Contains(pattern, "/") {
			for _, part := range strings.Split(path, "/") {
				if matched, _ := filepath.Match(pattern, part); matched {
					return true
				}
			}
		}
	}
	return false
}
