# MCP Memory Context

> **Development status:** This project is under active development. Its API, memory format, and configuration may change.

MCP Memory Context is a local Go server that gives an AI agent concise, project-specific explanations without exposing the project's memory files directly to that agent. It uses hierarchical Markdown memory documents, a local LLM, and the Model Context Protocol (MCP) over standard input/output.

An agent asks a natural-language question such as "How does the Ollama client work in this project?" The server selects relevant memory indexes and documents, asks the local LLM to synthesize an answer, and returns a structured MCP tool result.

## Features

- MCP JSON-RPC server over stdio.
- Hierarchical, local memory retrieval rooted at `memory/`.
- Server-side authorization: the LLM can select only memory documents declared by an index.
- Configurable local Ollama base URL, model, and system prompt.
- Structured, evidence-backed `gaps` returned only when declared in selected memory frontmatter.
- Integration tests for the local Ollama and end-to-end MCP paths.

## Requirements

- Go 1.27.3 or later.
- A running Ollama server.
- A model installed in Ollama.

## Configuration

Set the following environment variables before starting the server:

| Variable | Required | Description |
| --- | --- | --- |
| `OLLAMA_BASE_URL` | Yes | HTTP(S) URL of the Ollama server, for example `http://127.0.0.1:11434`. |
| `OLLAMA_MODEL` | Yes | Name of an installed Ollama model. |
| `MEMORY_ROOT` | No | Path to the memory directory. Defaults to `memory`. |

Example:

```bash
export OLLAMA_BASE_URL=http://127.0.0.1:11434
export OLLAMA_MODEL=qwen2.5-coder:7b
```

## Running the server

Start the MCP server from the repository root:

```bash
go run ./cmd/server
```

The server communicates through stdio. Configure your MCP client to launch the same command with the environment variables above.

## Available tool

### `explain_feature`

Accepts a natural-language question about the project:

```json
{
  "question": "How does the Ollama client work in this project?"
}
```

The result contains:

```json
{
  "answer": "A concise, evidence-backed feature explanation.",
  "sources": [
    "index.memory",
    "internal/ollama/index.memory",
    "internal/ollama/client.go.memory"
  ],
  "gaps": [
    "Only explicitly declared gaps appear here."
  ]
}
```

The `gaps` field is omitted when no selected memory document declares a gap.

## Memory format

Memory documents are Markdown files under `memory/`, with YAML frontmatter. Each document may declare explicit implementation gaps:

```markdown
---
type: source
gaps:
  - Bank retry policy is not implemented.
---

# Summary

...
```

Use `gaps: []`, or omit the key, when no gap is known. Missing information must not be recorded as a gap.

Project and package indexes declare the memory documents that may be selected. For example:

```markdown
---
type: index
gaps: []
---

- memory/internal/ollama/client.go.memory
```

## Testing

Run package tests:

```bash
go test ./internal/memory ./internal/explainer ./internal/ollama ./internal/mcp
```

Run local Ollama integration tests:

```bash
OLLAMA_INTEGRATION=1 \
OLLAMA_MODEL=qwen2.5-coder:7b \
go test -count=1 \
  -run '^(TestClientGenerateAgainstLocalOllama|TestServerExplainsOllamaClientThroughMCP)$' \
  ./internal/ollama ./internal/mcp
```
