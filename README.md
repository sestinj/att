# att

A tmux-based attention manager for [Claude Code](https://docs.anthropic.com/en/docs/claude-code). Monitor all your Claude Code sessions in one place and jump to whichever one needs you.

![att demo](https://github.com/sestinj/att/assets/demo.gif)

*An autonomous codebase built by the [Continued Software Factory](https://continue.dev/blueprint)*

## Why

If you run Claude Code across multiple projects, you end up with a bunch of terminal tabs — some waiting for input, some working, some idle. `att` gives you a single dashboard that surfaces exactly which sessions need your attention, so you can stop polling tabs and start responding.

## Install

```sh
curl -sSf https://raw.githubusercontent.com/sestinj/att/main/install.sh | sh
```

Or build from source:

```sh
go install github.com/sestinj/att@latest
```

**Requirements:** [tmux](https://github.com/tmux/tmux) (required), [fzf](https://github.com/junegunn/fzf) (optional, for fuzzy search)

## Setup

Register hooks so Claude Code can notify `att` of state changes:

```sh
att setup
```

This writes hook entries to `~/.claude/settings.json`. You only need to do this once.

## Usage

```sh
att
```

This opens a tmux session with a status bar showing your active Claude Code sessions. Sessions needing attention (asking a question, waiting for tool approval, etc.) are highlighted with `*`.

### Commands

| Command | Description |
|---------|-------------|
| `att` / `att feed` | Open the attention dashboard |
| `att start [path]` | Launch a new Claude Code session in a tmux window |
| `att setup` | Register hooks with Claude Code |

### Keybindings

| Key | Action |
|-----|--------|
| `M-]` / `M-[` | Next / previous session needing attention |
| `M-Enter` | Dismiss current session (until state changes) |
| `M-z` | Snooze session (15m, 30m, 1h, 2h, 4h, tomorrow) |
| `M-p` | Set priority (P0 Critical → P4 Default) |
| `M-i` | Pin session (always visible) |
| `M-/` | Fuzzy find sessions |
| `M-n` | Launch new Claude Code session |
| `M-a` | Toggle between attention-only and all sessions |
| `M-d` | Kill current session |
| `C-q` | Quit |

### Configuration

Create `~/.config/att/config.json`:

```json
{
  "command": "claude",
  "projects": ["/path/to/project1", "/path/to/project2"],
  "dir_command": "git rev-parse --show-toplevel"
}
```

| Field | Description | Default |
|-------|-------------|---------|
| `command` | Command to run in new windows | `claude` |
| `projects` | Project directories for `M-n` launcher | `[]` |
| `dir_command` | Shell command to resolve working directory | git root detection |

## How it works

`att` scans Claude Code's JSONL session files (`~/.claude/projects/`) every few seconds. It classifies each session's state — **Working**, **Idle**, **Asking**, **ToolPermission**, or **PlanMode** — and surfaces sessions that need your attention. State changes from Claude Code are also pushed instantly via hooks.

Sessions are managed through dismiss, snooze, and priority systems so you only see what matters. State is persisted in `~/.config/att/`.

## License

MIT
