package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testModel = "test-model"

func TestClientGenerateUsesEnvironmentBackedDefaults(t *testing.T) {
	originalBaseURL, originalModel := DefaultBaseURL, DefaultModel
	t.Cleanup(func() {
		DefaultBaseURL, DefaultModel = originalBaseURL, originalModel
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("request method = %q, want %q", r.Method, http.MethodPost)
		}
		if r.URL.Path != "/api/generate" {
			t.Errorf("request path = %q, want %q", r.URL.Path, "/api/generate")
		}

		var request struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
			System string `json:"system"`
			Stream *bool  `json:"stream"`
		}
		if err := decodeRequest(r, &request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if request.Model != testModel {
			t.Errorf("request model = %q, want %q", request.Model, testModel)
		}
		if request.Prompt != "summarize this code" {
			t.Errorf("request prompt = %q, want %q", request.Prompt, "summarize this code")
		}
		if request.System != DefaultSystemPrompt {
			t.Errorf("request system prompt = %q, want %q", request.System, DefaultSystemPrompt)
		}
		if request.Stream == nil || *request.Stream {
			t.Errorf("request stream = %v, want false", request.Stream)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"response":"generated summary","done":true}`)
	}))
	defer server.Close()

	DefaultBaseURL, DefaultModel = server.URL, testModel
	client, err := NewClient(Config{HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	response, err := client.Generate(context.Background(), "summarize this code")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if response != "generated summary" {
		t.Errorf("Generate() = %q, want %q", response, "generated summary")
	}
}

func TestNewClientRequiresEnvironmentBackedSettings(t *testing.T) {
	originalBaseURL, originalModel := DefaultBaseURL, DefaultModel
	t.Cleanup(func() {
		DefaultBaseURL, DefaultModel = originalBaseURL, originalModel
	})

	DefaultBaseURL, DefaultModel = "", ""
	if _, err := NewClient(Config{}); err == nil || !strings.Contains(err.Error(), "OLLAMA_BASE_URL") {
		t.Fatalf("NewClient() error = %v, want required OLLAMA_BASE_URL error", err)
	}

	DefaultBaseURL = "http://localhost:11434"
	if _, err := NewClient(Config{}); err == nil || !strings.Contains(err.Error(), "OLLAMA_MODEL") {
		t.Fatalf("NewClient() error = %v, want required OLLAMA_MODEL error", err)
	}
}

func TestClientGenerateUsesConfiguredModel(t *testing.T) {
	const model = "custom-model"
	const systemPrompt = "Respond using concise sentences."
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model  string `json:"model"`
			System string `json:"system"`
		}
		if err := decodeRequest(r, &request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if request.Model != model {
			t.Errorf("request model = %q, want %q", request.Model, model)
		}
		if request.System != systemPrompt {
			t.Errorf("request system prompt = %q, want %q", request.System, systemPrompt)
		}
		_, _ = fmt.Fprint(w, `{"response":"ok","done":true}`)
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:      server.URL,
		Model:        model,
		SystemPrompt: systemPrompt,
		HTTPClient:   server.Client(),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if _, err := client.Generate(context.Background(), "prompt"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestClientGenerateReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, `{"error":"model unavailable"}`)
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, Model: testModel, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.Generate(context.Background(), "prompt")
	if err == nil {
		t.Fatal("Generate() error = nil, want HTTP error")
	}
	if !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "model unavailable") {
		t.Errorf("Generate() error = %q, want status and server message", err)
	}
}

func TestClientGenerateRejectsInvalidResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"response":`},
		{name: "empty response", body: `{"done":true}`},
		{name: "empty body", body: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			client, err := NewClient(Config{BaseURL: server.URL, Model: testModel, HTTPClient: server.Client()})
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}

			if _, err := client.Generate(context.Background(), "prompt"); err == nil {
				t.Fatal("Generate() error = nil, want invalid response error")
			}
		})
	}
}

func TestClientGenerateHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		waitForRequestCancellation(r, started)
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, Model: testModel, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.Generate(ctx, "prompt")
		result <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not reach test server")
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Generate() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Generate() did not return after cancellation")
	}
}

func TestClientGenerateHonorsContextDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		waitForRequestCancellation(r, nil)
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, Model: testModel, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err = client.Generate(ctx, "prompt")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Generate() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestClientGenerateHonorsHTTPClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	defer server.Close()

	httpClient := server.Client()
	httpClient.Timeout = 20 * time.Millisecond
	client, err := NewClient(Config{BaseURL: server.URL, Model: testModel, HTTPClient: httpClient})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.Generate(context.Background(), "prompt")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Generate() error = %v, want context.DeadlineExceeded", err)
	}
}

func decodeRequest(r *http.Request, destination any) error {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(destination); err != nil {
		return fmt.Errorf("decode JSON request: %w", err)
	}
	return nil
}

func waitForRequestCancellation(r *http.Request, started chan<- struct{}) {
	_, _ = io.Copy(io.Discard, r.Body)
	if started != nil {
		close(started)
	}
	<-r.Context().Done()
}
