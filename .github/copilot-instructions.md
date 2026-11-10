# Master Prompt: Local MCP Server for Context Memory Optimization

## Project Context and Objective
Assist in building a high-performance Model Context Protocol (MCP) server written in Go. This MCP server acts as an intelligent local bridge between the primary editor/agent and a configurable LLM provider. It reads source code changes, generates structured memory summaries, and maintains synchronized companion memory files with minimal token overhead.

## Core Stack & Tooling Guidelines
- **Go Version:** Target Go 1.27.3 as configured in `go.mod`. Prefer current standard library features (`net/http.ServeMux`, `encoding/json/v2`, `context`).
- **Standard Library Priority:** Rely on Go standard library capabilities by default. Request explicit user authorization prior to introducing any third-party packages or modifying `go.mod`.
- **Gopls MCP Integration:** Apply the `gopls-workflow` skill for all Go programming tasks. Always execute workspace discovery, symbol searches (`go_search`), diagnostics checks (`go_diagnostics`), and package-level tests via `gopls`.
- **Language:** Write all source code, comments, identifiers, and project documentation in English.
- **LLM Provider Configuration:** Do not hardcode a provider, model, API base URL, endpoint, or credentials. Make the API base URL and endpoint fully configurable, along with the provider/API protocol, model, and authentication settings. Support provider-specific request and response formats through clear adapters or configuration, since OpenAI-compatible APIs and Ollama do not necessarily share identical schemas.

## Initial Project Architecture
Implement the project following this directory layout:

```text
my-mcp-memory/
├── cmd/
│   └── server/
│       └── main.go          # Binary entry point (stdio JSON-RPC transport listener)
├── internal/
│   ├── mcp/
│   │   ├── protocol.go      # JSON-RPC protocol parsing and tool dispatching
│   │   └── handlers.go      # Business logic handlers for MCP tool calls
│   ├── memory/
│   │   └── repository.go    # File operations for companion memory files
│   └── llm/
│       └── client.go        # Provider-neutral client for configurable LLM APIs
└── go.mod
```

## Engineering Practices & Development Rules

### SOLID Principles & Architecture
- Maintain strict boundary separation between layers.
- Depend on interfaces for HTTP callers, memory persistence engines, and protocol parsers.
- Ensure main entry points (`cmd/server/main.go`) remain lightweight, focusing solely on dependency injection and binary startup.

### Test-Driven Development (TDD)
- Author or update test suites to define expected unit behavior before implementing production changes.
- Ensure test coverage includes valid execution paths, edge scenarios, and error states.
- Keep integration tests that depend on external services in separate `*_integration_test.go` files, apart from unit tests; give each test file its own companion `.memory` file.
- Run package-specific tests using `go test [packagePath...]` upon resolving diagnostics.

### Git Version Control
- Perform local commits or git pushes exclusively when the user provides explicit directives.

### Agent Instructions and Skills
- Create, modify, or delete agent instruction or skill files only after receiving the user's explicit prior authorization for that change.

### Memory & Workspace Synchronization
- Store memory as Markdown files with YAML frontmatter. Each source and test file must have its own companion memory file under the project's `memory/` directory, named by appending `.memory` to the complete filename and preserving the source directory structure (for example, `internal/client/ollama.go` → `memory/internal/client/ollama.go.memory` and `internal/client/ollama_test.go` → `memory/internal/client/ollama_test.go.memory`). Keep implementation and tests in separate files.
- A source-file memory must summarize each exported function, list internal and external dependencies, and record critical implemented logic rules.
- A test-file memory must summarize the tests and scenarios in that file and describe their coverage of the related code. Do not claim measured coverage unless it has been measured.
- Keep memory documentation in simple, direct, concise English. Optimize it for LLM agents; omit redundant or irrelevant details.
- Keep each memory file synchronized with its corresponding code or test file. Do not require a central JSON index or duplicate the same memory in another format.

## Initial Task
Start by executing `go_workspace` and `go_vulncheck`. Next, establish the foundational structure by implementing a provider-neutral LLM API client using TDD. Its base URL, endpoint, provider/API protocol, model, and authentication must be configurable; do not assume a specific provider or model.