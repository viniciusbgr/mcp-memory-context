package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/ollama/ollama/api"
)

var (
	DefaultBaseURL = os.Getenv("OLLAMA_BASE_URL")
	DefaultModel   = os.Getenv("OLLAMA_MODEL")
)

const DefaultSystemPrompt = `You are the project's context specialist. Produce concise, actionable context for an agent implementing or changing a feature. Follow the requested output format exactly. For explanatory answers, use only these section titles when applicable: Objective, Flow, Rules and invariants, Extension points, and Relevant source files. Include only information supported by supplied project knowledge. Do not infer missing behavior, limitations, configuration, or implementation details. Never output a Gaps section: the server returns explicitly declared gaps separately. Do not state that something is absent unless the supplied project knowledge explicitly says it is absent. When a fact is uncertain, write "Not established by the supplied project knowledge" inline with that fact rather than guessing. Do not mention memory files or the retrieval process. Include a section only when it contains evidence-backed, actionable information. Omit empty sections and avoid generic advice.`

type Config struct {
	BaseURL      string
	Model        string
	SystemPrompt string
	HTTPClient   *http.Client
}

type generateAPI interface {
	Generate(context.Context, *api.GenerateRequest, api.GenerateResponseFunc) error
}

type Client struct {
	api          generateAPI
	model        string
	systemPrompt string
}

func NewClient(config Config) (*Client, error) {
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("OLLAMA_BASE_URL is required")
	}

	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Ollama base URL: %w", err)
	}
	if (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return nil, fmt.Errorf("invalid Ollama base URL %q: expected an http or https URL with a host", baseURL)
	}
	if parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return nil, fmt.Errorf("invalid Ollama base URL %q: query and fragment are not allowed", baseURL)
	}

	model := config.Model
	if model == "" {
		model = DefaultModel
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("OLLAMA_MODEL is required")
	}
	systemPrompt := config.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = DefaultSystemPrompt
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &Client{
		api:          api.NewClient(parsedURL, httpClient),
		model:        model,
		systemPrompt: systemPrompt,
	}, nil
}

func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	return c.generate(ctx, prompt, nil)
}

func (c *Client) GenerateJSON(ctx context.Context, prompt string) (string, error) {
	return c.generate(ctx, prompt, json.RawMessage(`"json"`))
}

func (c *Client) generate(ctx context.Context, prompt string, format json.RawMessage) (string, error) {
	stream := false
	think := api.ThinkValue{Value: false}
	request := &api.GenerateRequest{
		Model:  c.model,
		Prompt: prompt,
		System: c.systemPrompt,
		Stream: &stream,
		Think:  &think,
		Format: format,
	}

	var response *api.GenerateResponse
	err := c.api.Generate(ctx, request, func(result api.GenerateResponse) error {
		response = &result
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("Ollama generate request failed: %w", err)
	}
	if response == nil {
		return "", errors.New("Ollama returned no generate response")
	}
	if strings.TrimSpace(response.Response) == "" {
		return "", errors.New("Ollama returned an empty generate response")
	}

	return response.Response, nil
}
