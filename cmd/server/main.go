package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/viniciusbgr/mcp-memory-context/internal/explainer"
	"github.com/viniciusbgr/mcp-memory-context/internal/mcp"
	"github.com/viniciusbgr/mcp-memory-context/internal/memory"
	"github.com/viniciusbgr/mcp-memory-context/internal/ollama"
	"github.com/viniciusbgr/mcp-memory-context/internal/synchronizer"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	memoryRoot := os.Getenv("MEMORY_ROOT")
	if memoryRoot == "" {
		memoryRoot = "memory"
	}
	projectRoot := os.Getenv("PROJECT_ROOT")
	if projectRoot == "" {
		projectRoot = "."
	}
	repository, err := memory.NewRepository(memoryRoot)
	if err != nil {
		return fmt.Errorf("configure memory repository: %w", err)
	}
	sourceRepository, err := synchronizer.NewSourceRepository(projectRoot)
	if err != nil {
		return fmt.Errorf("configure source repository: %w", err)
	}
	if len(args) > 0 && args[0] != "serve" {
		return runCommand(ctx, args, sourceRepository, repository)
	}
	llm, err := ollama.NewClient(ollama.Config{})
	if err != nil {
		return fmt.Errorf("configure LLM client: %w", err)
	}
	synchronizationService, err := newSynchronizer(sourceRepository, repository, llm)
	if err != nil {
		return err
	}
	server := mcp.NewServerWithSynchronizer(explainer.NewService(repository, llm), synchronizationService)
	if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		return err
	}
	return nil
}

func runCommand(ctx context.Context, args []string, source *synchronizer.SourceRepository, store *memory.Repository) error {
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	full := flags.Bool("full", false, "regenerate all companion memory documents")
	fix := flags.Bool("fix", false, "reconcile memory indexes")
	workers := flags.Int("workers", 1, "maximum concurrent LLM requests")
	exclude := flags.String("exclude", "", "comma-separated source paths or patterns to exclude")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%s does not accept positional arguments", command)
	}
	options := synchronizer.RunOptions{
		Full:    *full,
		Fix:     *fix,
		Workers: *workers,
		DiscoverOptions: synchronizer.DiscoverOptions{
			Exclude: splitExclusions(*exclude),
		},
	}
	switch command {
	case "status":
		options.Status = true
		return encodeReport(ctx, synchronizer.NewManager(source, store, nil), options)
	case "sync":
		return runSyncCommand(ctx, source, store, options)
	case "watch":
		return watch(ctx, source, store, options)
	default:
		return fmt.Errorf("unknown command %q; use serve, sync, watch, or status", command)
	}
}

func runSyncCommand(ctx context.Context, source *synchronizer.SourceRepository, store *memory.Repository, options synchronizer.RunOptions) error {
	llm, err := ollama.NewClient(ollama.Config{})
	if err != nil {
		return fmt.Errorf("configure LLM client: %w", err)
	}
	service, err := newSynchronizer(source, store, llm)
	if err != nil {
		return err
	}
	return encodeReport(ctx, synchronizer.NewManager(source, store, service), options)
}

func encodeReport(ctx context.Context, manager *synchronizer.Manager, options synchronizer.RunOptions) error {
	report, err := manager.Run(ctx, options)
	if encodeErr := json.NewEncoder(os.Stdout).Encode(report); encodeErr != nil {
		return fmt.Errorf("write synchronization report: %w", encodeErr)
	}
	return err
}

func watch(ctx context.Context, source *synchronizer.SourceRepository, store *memory.Repository, options synchronizer.RunOptions) error {
	llm, err := ollama.NewClient(ollama.Config{})
	if err != nil {
		return fmt.Errorf("configure LLM client: %w", err)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	service, err := newSynchronizer(source, store, llm)
	if err != nil {
		return err
	}
	manager := synchronizer.NewManager(source, store, service)
	if err := encodeReport(ctx, manager, options); err != nil {
		return err
	}
	previous, err := fileStamps(ctx, source, options.DiscoverOptions)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var changedAt time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			current, err := fileStamps(ctx, source, options.DiscoverOptions)
			if err != nil {
				return err
			}
			if !sameStamps(previous, current) {
				previous = current
				changedAt = time.Now()
				continue
			}
			if !changedAt.IsZero() && time.Since(changedAt) >= 500*time.Millisecond {
				if err := encodeReport(ctx, manager, options); err != nil {
					return err
				}
				changedAt = time.Time{}
			}
		}
	}
}

func fileStamps(ctx context.Context, source *synchronizer.SourceRepository, options synchronizer.DiscoverOptions) (map[string]time.Time, error) {
	paths, err := source.Discover(ctx, options)
	if err != nil {
		return nil, err
	}
	stamps := make(map[string]time.Time, len(paths))
	for _, path := range paths {
		stamp, err := source.ModTime(ctx, path)
		if err != nil {
			return nil, err
		}
		stamps[path] = stamp
	}
	return stamps, nil
}

func sameStamps(left, right map[string]time.Time) bool {
	if len(left) != len(right) {
		return false
	}
	for path, stamp := range left {
		if other, ok := right[path]; !ok || !stamp.Equal(other) {
			return false
		}
	}
	return true
}

func splitExclusions(value string) []string {
	var paths []string
	for _, path := range strings.Split(value, ",") {
		if path = strings.TrimSpace(path); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func newSynchronizer(source *synchronizer.SourceRepository, store *memory.Repository, llm synchronizer.LLM) (*synchronizer.Service, error) {
	service, err := synchronizer.NewServiceWithOptions(source, store, llm, synchronizer.Options{
		ProvenanceFormat: os.Getenv("MEMORY_PROVENANCE_FORMAT"),
	})
	if err != nil {
		return nil, fmt.Errorf("configure memory synchronizer: %w", err)
	}
	return service, nil
}
