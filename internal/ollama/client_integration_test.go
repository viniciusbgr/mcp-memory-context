package ollama

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClientGenerateAgainstLocalOllama(t *testing.T) {
	if os.Getenv("OLLAMA_INTEGRATION") != "1" {
		t.Skip("set OLLAMA_INTEGRATION=1, OLLAMA_BASE_URL, and OLLAMA_MODEL to run the live-server check")
	}

	baseURL, baseURLSet := os.LookupEnv("OLLAMA_BASE_URL")
	model, modelSet := os.LookupEnv("OLLAMA_MODEL")
	if !baseURLSet || strings.TrimSpace(baseURL) == "" {
		t.Fatal("OLLAMA_BASE_URL must be set for the live-server check")
	}
	if !modelSet || strings.TrimSpace(model) == "" {
		t.Fatal("OLLAMA_MODEL must be set for the live-server check")
	}

	client, err := NewClient(Config{
		BaseURL: baseURL,
		Model:   model,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	response, err := client.Generate(ctx, "Reply with exactly: OK")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.TrimSpace(response) == "" {
		t.Fatal("Generate() returned an empty response")
	}
}
