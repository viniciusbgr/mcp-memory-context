package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/viniciusbgr/mcp-memory-context/internal/explainer"
	"github.com/viniciusbgr/mcp-memory-context/internal/mcp"
	"github.com/viniciusbgr/mcp-memory-context/internal/memory"
	"github.com/viniciusbgr/mcp-memory-context/internal/ollama"
)

func main() {
	memoryRoot := os.Getenv("MEMORY_ROOT")
	if memoryRoot == "" {
		memoryRoot = "memory"
	}
	repository, err := memory.NewRepository(memoryRoot)
	if err != nil {
		log.Fatal(fmt.Errorf("configure memory repository: %w", err))
	}
	llm, err := ollama.NewClient(ollama.Config{})
	if err != nil {
		log.Fatal(fmt.Errorf("configure LLM client: %w", err))
	}
	server := mcp.NewServer(explainer.NewService(repository, llm))
	if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
