# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it to **security@continue.dev**.

We will acknowledge your report within 48 hours and work with you to understand the scope and impact.

## Scope

- Command injection via configuration values or hook payloads
- Unauthorized access to Claude Code session data
- Path traversal in session file scanning
- tmux command injection

## Out of Scope

- Vulnerabilities in upstream dependencies (report to the dependency maintainer)
- Issues requiring existing local shell access (att runs locally by design)
- Denial of service via malformed JSONL files
