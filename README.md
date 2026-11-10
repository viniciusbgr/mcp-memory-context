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
| `PROJECT_ROOT` | No | Path containing Go source files that `sync_memory` may read. Defaults to the current directory. |
| `MEMORY_PROVENANCE_FORMAT` | No | `line` (default) or `repository` for `path#Lx-Ly` references in generated memory. |

Example:

```bash
export OLLAMA_BASE_URL=http://127.0.0.1:11434
export OLLAMA_MODEL=qwen2.5-coder:7b
```

## Running the server

Build the binary from the repository root:

```bash
make build
```

Start the MCP server directly:

```bash
./bin/mcp-memory-context
```

The server communicates through stdio. Configure your MCP client to launch the binary with the environment variables above.

## Synchronizing memory

The binary also synchronizes companion memory documents without an MCP tool call. All commands use `PROJECT_ROOT`, `MEMORY_ROOT`, and the configured local LLM. They emit a JSON report containing created, updated, unchanged, missing, stale, invalid, failed, and orphaned paths.

```bash
# Generate only missing, stale, or invalid Go memory documents.
./bin/mcp-memory-context sync --workers 2

# Regenerate every discovered Go document and rebuild indexes.
./bin/mcp-memory-context sync --full --workers 2

# Inspect state without writing; remove orphaned index references with --fix.
./bin/mcp-memory-context status
./bin/mcp-memory-context status --fix

# Poll for local changes and synchronize after a short debounce.
./bin/mcp-memory-context watch --workers 2
```

Discovery honors `.gitignore` and excludes hidden directories, generated files, `vendor`, and `memory`. Add comma-separated source patterns with `--exclude` when a project needs further exclusions. `sync --full` and `status --fix` rebuild the generated index references, removing orphaned paths and duplicate references. In CI, run `status` to report drift or `sync --full` after the local LLM is available; failures return a non-zero exit status.

### Copilot CLI

For a user-level Copilot CLI configuration, run this command from the repository root after setting the environment variables:

```bash
copilot mcp add mcp-memory-context \
  --env OLLAMA_BASE_URL="$OLLAMA_BASE_URL" \
  --env OLLAMA_MODEL="$OLLAMA_MODEL" \
  -- ./bin/mcp-memory-context
```

Verify the registration:

```bash
copilot mcp list
```

For workspace-level configuration, this repository's `.mcp.json` already registers the same stdio server:

```json
{
  "mcpServers": {
    "mcp-memory-context": {
      "command": "./bin/mcp-memory-context"
    }
  }
}
```

Run `make build` before starting Copilot CLI from the repository root. Export `OLLAMA_BASE_URL` and `OLLAMA_MODEL` so the workspace server receives them.

## Local Ollama with Docker

Unit tests do not require Ollama. Use Docker only for local integration tests or to run the MCP server with a local LLM:

```bash
docker compose up -d
docker compose logs -f ollama-model
```

The Compose file starts Ollama, then downloads `qwen2.5-coder:7b` into the persistent `ollama-data` volume. It binds Ollama only to `127.0.0.1:11434`. Set `OLLAMA_MODEL=qwen2.5-coder:7b` before running integration tests.

### Validated host configuration

The local LLM and integration tests have been validated on the following host configuration:

| Resource | Configuration |
| --- | --- |
| CPU | AMD Ryzen 7 2700, 16 logical CPUs |
| Memory | 16 GiB RAM, 4 GiB swap |
| GPU | NVIDIA GeForce RTX 2060 SUPER, 8 GiB VRAM |
| NVIDIA driver | 610.88 |
| Container runtime | NVIDIA Container Runtime |

The Compose configuration requests all NVIDIA GPUs for Ollama. A compatible NVIDIA driver and NVIDIA Container Toolkit are required for GPU acceleration. This is a validated environment, not a minimum hardware requirement.

## Available tools

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

### `sync_memory`

Creates or updates the companion memory file for a Go source or test file:

```json
{
  "source_path": "internal/ollama/client.go"
}
```

The server reads the source only below `PROJECT_ROOT`, extracts its Go API and import facts, asks the configured local LLM for a constrained JSON summary, validates that summary against the parsed source, and renders the final Markdown itself before writing only below `MEMORY_ROOT`. The corresponding package and project indexes are created or updated as needed. The result contains the generated memory path and whether it was created or updated:

```json
{
  "memory_path": "internal/ollama/client.go.memory",
  "action": "updated"
}
```

The LLM receives source text and existing memory as data but never receives direct filesystem access. The server rejects responses with missing or unknown exported symbols, mismatched imports, unverified gaps, code blocks, excessive size, invalid JSON, unsafe paths, or source files outside the configured project root without replacing the existing memory document. Rules and test scenarios must cite verified source-line evidence; final documents use canonical line references for summaries and exported symbols, and include a schema version and source-content hash.

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
go test ./internal/memory ./internal/explainer ./internal/ollama ./internal/mcp ./internal/synchronizer
```

Run local Ollama integration tests:

```bash
OLLAMA_INTEGRATION=1 \
OLLAMA_MODEL=qwen2.5-coder:7b \
go test -count=1 \
  -run '^(TestClientGenerateAgainstLocalOllama|TestServerExplainsOllamaClientThroughMCP)$' \
  ./internal/ollama ./internal/mcp
```
