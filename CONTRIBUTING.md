# Contributing to att

Thanks for your interest in contributing! Here's how to get started.

## Development Setup

```sh
git clone https://github.com/sestinj/att.git
cd att
go build -o att .
```

**Requirements:**
- Go 1.24+
- tmux (for running and testing)

### Running Tests

```sh
go test ./...
```

Some tests require tmux to be running. Integration and E2E tests are in `internal/tmux/`.

## Project Structure

```
att/
├── main.go              # Entry point
├── cmd/                 # CLI commands (Cobra)
│   ├── root.go
│   ├── feed.go          # Dashboard command
│   ├── start.go         # Launch new session
│   ├── setup.go         # Register hooks
│   └── hook.go          # Handle state changes
├── internal/
│   ├── claude/          # Claude Code session parsing
│   ├── config/          # Configuration management
│   └── tmux/            # tmux integration + feed rendering
└── docs/                # Design docs
```

## Making Changes

1. Fork and create a branch from `main`
2. Make your changes
3. Run `go test ./...` to verify
4. Commit using [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, etc.)
5. Open a PR against `main`

## Reporting Bugs

Use the [bug report template](https://github.com/sestinj/att/issues/new?template=bug_report.yml) to file issues.
