<p align="center">
  <a href="https://github.com/alibaba/open-code-review">
    <img src="imgs/logo.svg" alt="OpenCodeReview logo" width="240" height="240">
  </a>
</p>
<p align="center">The open source AI code review agent.</p>
<p align="center">
  <a href="https://www.npmjs.com/package/@alibaba-group/open-code-review"><img alt="npm" src="https://img.shields.io/npm/v/@alibaba-group/open-code-review?style=flat-square" /></a>
  <a href="https://github.com/alibaba/open-code-review/actions/workflows/release.yml"><img alt="Build status" src="https://img.shields.io/github/actions/workflow/status/alibaba/open-code-review/release.yml?style=flat-square&branch=main" /></a>
  <a href="https://github.com/alibaba/open-code-review/blob/main/LICENSE"><img alt="License" src="https://img.shields.io/github/license/alibaba/open-code-review?style=flat-square" /></a>
</p>
<p align="center">
  English | <a href="README.zh-CN.md">简体中文</a>
</p>

---

AI-powered code review CLI that reads Git diffs, sends changed files to a configurable LLM via an agent with tool-use capabilities, and generates structured review comments with line-level precision.

The agent can read full file contents, search the codebase, inspect other changed files for context, and produce deep reviews — not just surface-level diff feedback.

![Open Benchmark](imgs/open-benchmark.png)

## Install

### Via NPM (Recommended)

```bash
npm install -g @alibaba-group/open-code-review
```

After installation, the `ocr` command is available globally.

### From GitHub Release

Download the latest binary from [GitHub Releases](https://github.com/alibaba/open-code-review/releases):

```bash
# macOS (Apple Silicon)
curl -Lo ocr https://github.com/alibaba/open-code-review/releases/latest/download/opencodereview-darwin-arm64
chmod +x ocr && sudo mv ocr /usr/local/bin/ocr

# macOS (Intel)
curl -Lo ocr https://github.com/alibaba/open-code-review/releases/latest/download/opencodereview-darwin-amd64
chmod +x ocr && sudo mv ocr /usr/local/bin/ocr

# Linux (x86_64)
curl -Lo ocr https://github.com/alibaba/open-code-review/releases/latest/download/opencodereview-linux-amd64
chmod +x ocr && sudo mv ocr /usr/local/bin/ocr

# Linux (ARM64)
curl -Lo ocr https://github.com/alibaba/open-code-review/releases/latest/download/opencodereview-linux-arm64
chmod +x ocr && sudo mv ocr /usr/local/bin/ocr
```

### From Source

```bash
git clone https://github.com/alibaba/open-code-review.git
cd open-code-review
make build
sudo cp dist/opencodereview /usr/local/bin/ocr
```

## Quick Start

### 1. Configure LLM

**You must configure an LLM before reviewing code.**

```bash
# Option A: Interactive config
ocr config set llm.url https://api.anthropic.com/v1/messages
ocr config set llm.auth_token your-api-key-here
ocr config set llm.model claude-opus-4-6
ocr config set llm.use_anthropic true

# Option B: Environment variables (highest priority)
export OCR_LLM_URL=https://api.anthropic.com/v1/messages
export OCR_LLM_TOKEN=your-api-key-here
export OCR_LLM_MODEL=claude-opus-4-6
export OCR_USE_ANTHROPIC=true
```

Config is stored in `~/.opencodereview/config.json`.

The tool also falls back to Claude Code environment variables (`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_MODEL`) and parses `~/.zshrc` / `~/.bashrc` for those exports.

### 2. Test Connectivity

```bash
ocr llm test
```

### 3. Review

```bash
cd your-project

# Workspace mode — review all staged, unstaged, and untracked changes
ocr review

# Branch range — compare two refs
ocr review --from main --to feature-branch

# Single commit
ocr review --commit abc123
```

## Commands

| Command | Alias | Description |
|---------|-------|-------------|
| `ocr review` | `ocr r` | Start a code review |
| `ocr rules check <file>` | — | Preview which review rule applies to a file path |
| `ocr config set <key> <value>` | — | Set configuration values |
| `ocr llm test` | — | Test LLM connectivity |
| `ocr viewer` | `ocr v` | Launch WebUI session viewer on `localhost:5483` |
| `ocr version` | — | Show version info |

### `ocr review` Flags

| Flag | Shorthand | Default | Description |
|------|-----------|---------|-------------|
| `--repo` | — | current dir | Git repository root |
| `--from` | — | — | Source ref (e.g., `main`) |
| `--to` | — | — | Target ref (e.g., `feature-branch`) |
| `--commit` | `-c` | — | Single commit to review |
| `--preview` | `-p` | `false` | Preview which files will be reviewed without running the LLM |
| `--format` | `-f` | `text` | Output format: `text` or `json` |
| `--concurrency` | — | `8` | Max concurrent file reviews |
| `--timeout` | — | `10` | Concurrent task timeout in minutes |
| `--audience` | — | `human` | `human` (show progress) or `agent` (summary only) |
| `--rule` | — | — | Path to custom JSON review rules |
| `--tools` | — | — | Path to custom JSON tools config |

## Examples

```bash
# Preview which files will be reviewed (no LLM calls)
ocr review --preview
ocr review -c abc123 -p

# Review workspace changes with default settings
ocr review

# Review branch diff with higher concurrency
ocr review --from main --to my-feature --concurrency 4

# Review a specific commit with verbose JSON output
ocr review --commit abc123 --format json --audience agent

# Use custom review rules
ocr review --rule /path/to/my-rules.json

# Preview which rule applies to a file
ocr rules check src/main/java/com/example/Foo.java
ocr rules check --rule custom.json src/main/resources/mapper/UserMapper.xml

# View review session history in browser
ocr viewer
ocr viewer --addr :3000
```

## Review Rules

OCR resolves review rules using a four-layer priority chain. Each layer uses first-match-wins: if a file path matches a pattern, that rule is used; otherwise it falls through to the next layer.

| Priority | Source | Path | Description |
|----------|--------|------|-------------|
| 1 (highest) | `--rule` flag | User-specified path | CLI explicit override |
| 2 | Project config | `<repoDir>/.opencodereview/rule.json` | Per-project rules, can be committed to git |
| 3 | Global config | `~/.opencodereview/rule.json` | User-wide personal preferences |
| 4 (lowest) | System default | Embedded `system_rules.json` | Built-in rules covering common languages and file types |

### Rule File Format

Layers 1–3 share the same JSON format:

```json
{
  "rules": [
    {
      "path": "force-api/**/*.java",
      "rule": "All new methods must validate required parameters for null values"
    },
    {
      "path": "**/*mapper*.xml",
      "rule": "Check SQL for injection risks, parameter errors, and missing closing tags"
    }
  ]
}
```

- `path` supports `**` recursive matching and `{java,kt}` brace expansion.
- Within each layer, rules are evaluated in declaration order — the first match wins.
- If a rule file does not exist, it is silently skipped.

## Architecture

The review agent follows a **three-phase workflow**:

1. **Plan Phase** — For changes exceeding 50 lines, the agent performs risk analysis before reviewing. Smaller diffs skip directly to the main phase.
2. **Main Task Loop** — Each changed file gets its own goroutine. The LLM interacts with built-in tools (read files, search code, read diffs, submit comments) in a conversation loop until it calls `task_done`.
3. **Memory Compression** — When prompt context exceeds token thresholds (60% async, 80% sync), the agent uses three-zone partitioning (frozen / compress / active) to manage context window size.

### Key Design Decisions

- **Concurrent per-file processing** — Files are reviewed in parallel (default 8 workers). Timeout prevents any single file from blocking others.
- **Dual protocol support** — Both Anthropic Messages API and OpenAI Chat Completions API are supported, with automatic URL normalization.
- **Tool-use agent** — The LLM has access to domain-specific tools (`code_search`, `file_read`, `code_comment`, `file_find`, `file_read_diff`), enabling cross-referential context-aware reviews rather than isolated diff scanning.

## Configuration Reference

Config file: `~/.opencodereview/config.json`

| Key | Type | Example |
|-----|------|---------|
| `llm.url` | string | `https://api.openai.com/v1/chat/completions` |
| `llm.auth_token` | string | `sk-xxxxxxx` |
| `llm.model` | string | `claude-opus-4-6` |
| `llm.use_anthropic` | boolean | `true` \| `false` |
| `language` | string | `English` \| `Chinese` (default: Chinese) |
| `telemetry.enabled` | boolean | `true` \| `false` |
| `telemetry.exporter` | string | `console` \| `otlp` |
| `telemetry.otlp_endpoint` | string | OTLP collector address |
| `telemetry.content_logging` | boolean | Include prompts in telemetry |

Environment variables take precedence over the config file.

### Environment Variables

| Variable | Purpose |
|----------|---------|
| `OCR_LLM_URL` | LLM API endpoint URL |
| `OCR_LLM_TOKEN` | API key / auth token |
| `OCR_LLM_MODEL` | Model name |
| `OCR_USE_ANTHROPIC` | `true` = Anthropic, `false` = OpenAI |

### Template Parameters

Internal defaults defined in `internal/config/template/task_template.json`:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `MAX_TOKENS` | 58888 | Max tokens per LLM request |
| `MAX_TOOL_REQUEST_TIMES` | 20 | Max tool-use iterations per file |
| `PLAN_MODE_LINE_THRESHOLD` | 50 | Skip plan phase below this line count |
| `TOOL_REQUEST_WAIT_TIME_MS` | 10000 | Per-tool-request timeout |

## Built-in Tools

Tools the LLM agent can invoke during review:

| Tool | Phases | Purpose |
|------|--------|---------|
| `task_done` | main_task | Terminate the review (DONE/FAILED) |
| `code_comment` | main_task | Submit a line-level review comment |
| `file_read` | main_task | Read file content at a line range |
| `code_search` | plan + main | Search text/regex across files |
| `file_read_diff` | plan + main | View diff content for other changed files |
| `file_find` | plan + main | Find files by filename keyword |

## System Review Rules

Built-in glob-pattern-matched review checklists per file type, defined in `internal/config/rules/system_rules.json`:

| Pattern | Focus Areas |
|---------|-------------|
| `*.java` | NPE risks, dead loops, switch fallthrough, N+1 queries, thread safety |
| `*.{ts,js,tsx,jsx}` | Quality, React best practices, async norms, XSS/security |
| `*.kt` | Null safety, coroutine usage, idiomatic patterns |
| `*{go,py,ets,lua,dart,swift,groovy}` | Logic bugs, typos |
| `*{cpp,cc,hpp}` | Smart pointers, RAII, STL, const correctness |
| `*.c` | malloc/free pairing, buffer overflow |
| `pom.xml` / `build.gradle` | SNAPSHOT version prevention |
| `package.json` | Latest/wildcard versions, dependency conflicts |
| `*mapper*.xml` / `*dao*.xml` | SQL injection, performance, logic errors |
| `*.properties` | Typo detection, duplicate keys, security issues |

Override with `--rule path/to/rules.json`.

## Telemetry

OpenTelemetry integration for observability (spans, metrics). Disabled by default.

```bash
ocr config set telemetry.enabled true
ocr config set telemetry.exporter otlp
ocr config set telemetry.otlp_endpoint localhost:4317
```

Set `telemetry.content_logging` to include LLM prompts and responses in exported data.

## Development

```bash
make build      # Build for current platform
make test       # Run tests with race detection
make clean      # Remove dist/
make build-all  # Cross-compile (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64)
make dist       # Full release pipeline
```

## License

[Apache-2.0](LICENSE) — Copyright 2026 Alibaba
