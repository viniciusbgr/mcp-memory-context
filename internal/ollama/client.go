package ollama

import (
	"context"
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

type Config struct {
	BaseURL    string
	Model      string
	HTTPClient *http.Client
}

type generateAPI interface {
	Generate(context.Context, *api.GenerateRequest, api.GenerateResponseFunc) error
}

type Client struct {
	api   generateAPI
	model string
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

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &Client{
		api:   api.NewClient(parsedURL, httpClient),
		model: model,
	}, nil
}

func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	stream := false
	request := &api.GenerateRequest{
		Model:  c.model,
		Prompt: prompt,
		Stream: &stream,
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
