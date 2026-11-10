// Package explainer turns hierarchical project memory into feature explanations.
package explainer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/viniciusbgr/mcp-memory-context/internal/memory"
)

type LLM interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

type ExplainFeatureInput struct {
	Question    string `json:"question"`
	TokenBudget int    `json:"token_budget,omitempty"`
}

type ExplainFeatureOutput struct {
	Answer  string   `json:"answer"`
	Sources []string `json:"sources"`
	Gaps    []string `json:"gaps,omitempty"`
}

type Service struct {
	memory memory.Reader
	llm    LLM
}

func NewService(memoryReader memory.Reader, llm LLM) *Service {
	return &Service{memory: memoryReader, llm: llm}
}

func (s *Service) ExplainFeature(ctx context.Context, input ExplainFeatureInput) (ExplainFeatureOutput, error) {
	if strings.TrimSpace(input.Question) == "" {
		return ExplainFeatureOutput{}, errors.New("question is required")
	}
	if s.memory == nil {
		return ExplainFeatureOutput{}, errors.New("memory reader is required")
	}
	if s.llm == nil {
		return ExplainFeatureOutput{}, errors.New("LLM is required")
	}

	projectIndex, err := s.memory.ReadProjectIndex(ctx)
	if err != nil {
		return ExplainFeatureOutput{}, fmt.Errorf("read project index: %w", err)
	}
	indexes, err := s.selectPaths(ctx, planningPrompt(input.Question, projectIndex.Content), "indexes")
	if err != nil {
		return ExplainFeatureOutput{}, fmt.Errorf("select memory indexes: %w", err)
	}

	sources := []string{projectIndex.Path}
	var selectedMemory []memory.Document
	for _, indexPath := range indexes {
		index, err := s.memory.ReadDeclared(ctx, projectIndex.Path, indexPath)
		if err != nil {
			return ExplainFeatureOutput{}, err
		}
		sources = append(sources, index.Path)

		files, err := s.selectPaths(ctx, selectionPrompt(input.Question, index.Content), "files")
		if err != nil {
			return ExplainFeatureOutput{}, fmt.Errorf("select memory documents from %q: %w", index.Path, err)
		}
		for _, path := range files {
			document, err := s.memory.ReadDeclared(ctx, index.Path, path)
			if err != nil {
				return ExplainFeatureOutput{}, err
			}
			selectedMemory = append(selectedMemory, document)
			sources = append(sources, document.Path)
		}
	}
	if len(selectedMemory) == 0 {
		return ExplainFeatureOutput{}, errors.New("no memory documents selected")
	}

	answer, err := s.llm.Generate(ctx, answerPrompt(input.Question, selectedMemory))
	if err != nil {
		return ExplainFeatureOutput{}, fmt.Errorf("generate feature explanation: %w", err)
	}
	if strings.TrimSpace(answer) == "" {
		return ExplainFeatureOutput{}, errors.New("LLM returned an empty feature explanation")
	}
	return ExplainFeatureOutput{
		Answer:  answer,
		Sources: sources,
		Gaps:    collectGaps(selectedMemory),
	}, nil
}

func (s *Service) selectPaths(ctx context.Context, prompt, field string) ([]string, error) {
	response, err := s.llm.Generate(ctx, prompt)
	if err != nil {
		return nil, err
	}
	response, err = extractJSON(response)
	if err != nil {
		return nil, err
	}
	var selection map[string][]string
	if err := json.Unmarshal([]byte(response), &selection); err != nil {
		return nil, fmt.Errorf("parse LLM JSON selection: %w", err)
	}
	paths := selection[field]
	if len(paths) == 0 {
		return nil, fmt.Errorf("LLM selected no %s", field)
	}
	return paths, nil
}

func extractJSON(response string) (string, error) {
	response = strings.TrimSpace(response)
	if !strings.HasPrefix(response, "```") {
		return response, nil
	}

	lines := strings.Split(response, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[len(lines)-1]) != "```" {
		return "", errors.New("LLM JSON selection has an invalid code fence")
	}
	language := strings.TrimSpace(strings.TrimPrefix(lines[0], "```"))
	if language != "" && language != "json" {
		return "", fmt.Errorf("LLM JSON selection uses unsupported code fence language %q", language)
	}
	return strings.Join(lines[1:len(lines)-1], "\n"), nil
}

func planningPrompt(question, projectIndex string) string {
	return "You are a project-memory retrieval planner.\n" +
		"Select the relevant index memory files for this question. Return JSON only as {\"indexes\":[\"path/to/index.memory\"]}.\n" +
		"Select only paths explicitly listed in the project memory index.\n\nQuestion:\n" + question +
		"\n\nProject memory index:\n" + projectIndex
}

func selectionPrompt(question, packageIndex string) string {
	return "You are a project-memory retrieval planner.\n" +
		"Select the relevant memory files for this question. Return JSON only as {\"files\":[\"path/to/file.memory\"]}.\n" +
		"Select only paths explicitly listed in the supplied index.\n\nQuestion:\n" + question +
		"\n\nMemory index:\n" + packageIndex
}

func answerPrompt(question string, documents []memory.Document) string {
	var context strings.Builder
	for _, document := range documents {
		fmt.Fprintf(&context, "\n\n--- %s ---\n%s", document.Path, document.Content)
	}
	return "Answer the project question using only the supplied project memory. Explain the feature flow, architecture, business rules, dependencies, entry points, and relevant source files. Do not include a Gaps section: the server returns explicitly declared gaps separately. State uncertainty explicitly. Do not mention memory files or this retrieval process.\n\nQuestion:\n" +
		question + "\n\nProject knowledge:" + context.String()
}

func collectGaps(documents []memory.Document) []string {
	seen := make(map[string]struct{})
	var gaps []string
	for _, document := range documents {
		for _, gap := range document.Gaps {
			if _, ok := seen[gap]; ok {
				continue
			}
			seen[gap] = struct{}{}
			gaps = append(gaps, gap)
		}
	}
	return gaps
}
