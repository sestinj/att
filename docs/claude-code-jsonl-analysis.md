# Claude Code JSONL Session File Format

Comprehensive analysis of the JSONL session files produced by Claude Code (v2.1.x), derived from examining dozens of real session files on disk at `~/.claude/projects/`.

## File Location and Structure

### Directory Layout

```
~/.claude/projects/
  -Users-nate-gh-sestinj-att/           # project dir (path with - for /)
    sessions-index.json                  # session index metadata
    6ba5da16-5800-45b6-a1e2-...jsonl     # main session file
    6ba5da16-5800-45b6-a1e2-.../         # per-session directory
      subagents/                         # subagent (sidechain) sessions
        agent-a68da48c3a1140234.jsonl
        agent-acompact-d385b8aee29d4f7e.jsonl  # compact agent files
```

- Project directories are named with the workspace path, replacing `/` with `-`
- Each session file is a UUID `.jsonl`
- One line per JSON object, newline-delimited
- Session files can grow very large (some exceed 30MB)

### sessions-index.json

A companion file that indexes sessions per project directory:

```json
{
  "version": 1,
  "entries": [
    {
      "sessionId": "fdf88fa2-302a-4269-b8ac-bc370bf21f2a",
      "fullPath": "~/.claude/projects/-Users-nate/fdf88fa2-....jsonl",
      "fileMtime": 1769835728245,
      "firstPrompt": "! pwd",
      "summary": "Local Directory Check: User in Home Directory",
      "messageCount": 2,
      "created": "2026-01-09T01:58:12.377Z",
      "modified": "2026-01-09T01:58:12.377Z",
      "gitBranch": "",
      "projectPath": "/Users/nate",
      "isSidechain": false
    }
  ],
  "originalPath": "/Users/nate"
}
```

---

## Top-Level Entry Types

Every line in a JSONL file is a JSON object with a `type` field. The following top-level types exist:

| Type | Description | Frequency |
|------|-------------|-----------|
| `progress` | Tool execution progress, hooks, MCP calls, agent activity | Very high |
| `assistant` | Model response (thinking, text, tool_use) | High |
| `user` | User input or tool results returned to the model | High |
| `system` | System events (hooks summary, turn duration, errors, compaction) | Medium |
| `file-history-snapshot` | File backup snapshots for undo/restore | Medium |
| `queue-operation` | Task/agent queue enqueue/dequeue events | Low |
| `pr-link` | GitHub PR association with the session | Low |
| `custom-title` | User-defined session title | Rare |
| `agent-name` | Named agent sessions | Rare |

---

## Common Fields (Envelope)

Most entry types (except `file-history-snapshot`, `queue-operation`, `pr-link`, `custom-title`, `agent-name`) share a common envelope of fields:

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Entry type (see table above) |
| `parentUuid` | string\|null | UUID of the logically previous entry (forms a chain) |
| `uuid` | string | Unique identifier for this entry |
| `isSidechain` | boolean | `true` for subagent/sidechain entries; `false` for main conversation |
| `userType` | string | Always `"external"` in observed data |
| `cwd` | string | Current working directory of the Claude Code process |
| `sessionId` | string | UUID matching the JSONL filename |
| `version` | string | Claude Code version (e.g. `"2.1.63"`) |
| `gitBranch` | string | Current git branch at time of entry |
| `timestamp` | string | ISO 8601 timestamp |
| `slug` | string | Human-readable session slug (e.g. `"sparkling-herding-pumpkin"`) |
| `teamName` | string | Team name if the session is part of a team |
| `agentId` | string | Agent ID (present in subagent JSONL files) |

Not every entry has every field -- `slug`, `teamName`, and `agentId` are only present when relevant.

---

## Entry Type Details

### 1. `user` -- User Messages and Tool Results

User entries represent either direct human input or tool results being returned to the model.

#### Common Fields

| Field | Type | Description |
|-------|------|-------------|
| `message` | object | The message payload |
| `message.role` | string | Always `"user"` |
| `message.content` | string\|array | Message content (see below) |
| `permissionMode` | string | Permission mode (e.g. `"bypassPermissions"`) |
| `todos` | array | Todo items associated with the turn (usually empty `[]`) |
| `planContent` | string | Plan markdown when resuming from plan mode |
| `toolUseResult` | object | Structured metadata about the tool result (see below) |
| `sourceToolAssistantUUID` | string | UUID of the assistant entry that invoked this tool |
| `sourceToolUseID` | string | Tool use ID linking to the assistant's tool_use block |
| `imagePasteIds` | array | Indices of pasted images (e.g. `[1]`) |
| `isCompactSummary` | boolean | `true` when this is a compaction summary message |
| `isVisibleInTranscriptOnly` | boolean | `true` for entries only shown in transcript view |
| `isMeta` | boolean | `true` for meta/system-injected user messages |
| `mcpMeta` | object | MCP-specific metadata (structured content from MCP tools) |

#### Content Formats

**String content** (direct user text input):
```json
{
  "type": "user",
  "message": {
    "role": "user",
    "content": "what are all the options you have with your ask user question tool"
  }
}
```

**Array content** (structured blocks):
```json
{
  "type": "user",
  "message": {
    "role": "user",
    "content": [
      {"type": "text", "text": "look at this screenshot"},
      {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "iVBOR..."}}
    ]
  }
}
```

**Tool result content**:
```json
{
  "type": "user",
  "message": {
    "role": "user",
    "content": [
      {
        "tool_use_id": "toolu_01VKKmgEPA1Ar4SzzykVqTAh",
        "type": "tool_result",
        "content": "ok  github.com/sestinj/att/internal/tmux  0.511s",
        "is_error": false
      }
    ]
  }
}
```

Tool result content can also be an array of blocks:
```json
{
  "content": [
    {"type": "tool_result", "tool_use_id": "...", "content": [
      {"type": "tool_reference", "tool_name": "AskUserQuestion"}
    ]}
  ]
}
```

#### Content Block Types (User)

| Block Type | Description |
|------------|-------------|
| `text` | Plain text from user or tool output |
| `image` | Image data (screenshots, pastes) with `source.type`, `source.media_type`, `source.data` |
| `tool_result` | Result of a tool invocation, with `tool_use_id`, `content`, `is_error` |
| `tool_reference` | Reference to a discovered tool (from ToolSearch), with `tool_name` |

#### toolUseResult

The `toolUseResult` field provides structured metadata about tool execution. Its shape varies by tool. Common patterns:

**Bash tool results:**
```json
{
  "stdout": "ok  github.com/sestinj/att/internal/tmux  0.511s",
  "stderr": "",
  "interrupted": false,
  "isImage": false,
  "noOutputExpected": false
}
```

**Read tool results:**
```json
{
  "type": "text",
  "file": {
    "filePath": "/path/to/file.go",
    "content": "...",
    "numLines": 35,
    "startLine": 425,
    "totalLines": 1011
  }
}
```

**Edit tool results:**
```json
{
  "filePath": "/path/to/file.go",
  "oldString": "...",
  "newString": "...",
  "originalFile": "...",
  "structuredPatch": [...],
  "userModified": false,
  "replaceAll": false
}
```

**ToolSearch results:**
```json
{
  "matches": ["AskUserQuestion"],
  "query": "select:AskUserQuestion",
  "total_deferred_tools": 67
}
```

**All observed toolUseResult keys** (varies by tool):
`stdout`, `stderr`, `interrupted`, `isImage`, `noOutputExpected`, `type`, `file`, `filePath`, `content`, `oldString`, `newString`, `originalFile`, `structuredPatch`, `userModified`, `replaceAll`, `matches`, `query`, `total_deferred_tools`, `success`, `status`, `result`, `results`, `name`, `description`, `command`, `commandName`, `taskId`, `task_id`, `tasks`, `task`, `task_type`, `statusChange`, `updatedFields`, `agentId`, `agent_id`, `agent_type`, `team_name`, `team_file_path`, `lead_agent_id`, `teammate_id`, `recipients`, `plan_mode_required`, `model`, `routing`, `url`, `prompt`, `answers`, `questions`, `message`, `request_id`, `target`, `code`, `codeText`, `color`, `bytes`, `numFiles`, `numLines`, `filenames`, `appliedLimit`, `truncated`, `durationMs`, `durationSeconds`, `totalDurationMs`, `totalTokens`, `totalToolUseCount`, `usage`, `backgroundTaskId`, `isAsync`, `is_splitpane`, `canReadOutputFile`, `outputFile`, `persistedOutputPath`, `persistedOutputSize`, `retrieval_status`, `returnCodeInterpretation`, `tmux_session_name`, `tmux_window_name`, `tmux_pane_id`, `mode`, `mcpMeta`.

---

### 2. `assistant` -- Model Responses

Assistant entries contain the model's response, which may include thinking, text, and tool_use blocks.

#### Fields

| Field | Type | Description |
|-------|------|-------------|
| `message` | object | The Anthropic API message object |
| `message.model` | string | Model ID (e.g. `"claude-opus-4-6"`, `"claude-haiku-4-5-20251001"`) |
| `message.id` | string | Anthropic message ID (e.g. `"msg_01EyMD1Uoo93mKtRAQe31JZn"`) |
| `message.type` | string | Always `"message"` |
| `message.role` | string | Always `"assistant"` |
| `message.content` | array | Array of content blocks |
| `message.stop_reason` | string\|null | Why the model stopped generating |
| `message.stop_sequence` | string\|null | The stop sequence that triggered (usually `null`) |
| `message.usage` | object | Token usage statistics |
| `requestId` | string | Anthropic API request ID |

#### stop_reason Values

| Value | Meaning | State Implication |
|-------|---------|-------------------|
| `"end_turn"` | Model finished its response naturally | **Idle** -- waiting for user input |
| `"tool_use"` | Model wants to call a tool | **ToolPermission** or **Working** (depends on progress entries) |
| `"stop_sequence"` | Model hit a stop sequence | **Idle** |
| `null` | Streaming partial or old-version final entry | Depends on content blocks |

#### Content Block Types (Assistant)

| Block Type | Fields | Description |
|------------|--------|-------------|
| `thinking` | `thinking` (string), `signature` (string) | Extended thinking block (model's internal reasoning) |
| `text` | `text` (string) | Text response to the user |
| `tool_use` | `id`, `name`, `input`, `caller` | Tool invocation request |

#### Streaming Behavior

Multiple `assistant` entries can share the same `message.id` (same `requestId`). This happens because Claude Code writes partial results as they stream in:

1. First entry: `stop_reason: null`, `content: [{"type": "thinking", ...}]`
2. Second entry: `stop_reason: null`, `content: [{"type": "text", ...}]`
3. Third entry: `stop_reason: "tool_use"`, `content: [{"type": "tool_use", ...}]` (final)

The last entry for a given request has the real `stop_reason`.

#### tool_use Block Structure

```json
{
  "type": "tool_use",
  "id": "toolu_01VKKmgEPA1Ar4SzzykVqTAh",
  "name": "Read",
  "input": {
    "file_path": "/path/to/file.go",
    "offset": 425,
    "limit": 35
  },
  "caller": {
    "type": "direct"
  }
}
```

#### Observed Tool Names

`Agent`, `AskUserQuestion`, `Bash`, `Edit`, `EnterPlanMode`, `ExitPlanMode`, `Glob`, `Grep`, `Read`, `SendMessage`, `Skill`, `Task`, `TaskCreate`, `TaskList`, `TaskOutput`, `TaskStop`, `TaskUpdate`, `TeamCreate`, `TeamDelete`, `ToolSearch`, `WebFetch`, `WebSearch`, `Write`, plus MCP tools like `mcp__posthog__query-run`.

#### Special Tool Names for State Detection

| Tool Name | State | Description |
|-----------|-------|-------------|
| `AskUserQuestion` | **Asking** | Model is asking the user a question |
| `ExitPlanMode` | **PlanMode** | Model is presenting a plan for approval |
| Any other tool | **ToolPermission** | Until progress entries indicate execution |

#### Usage Object

```json
{
  "input_tokens": 3,
  "cache_creation_input_tokens": 5396,
  "cache_read_input_tokens": 22284,
  "output_tokens": 181,
  "server_tool_use": {
    "web_search_requests": 0,
    "web_fetch_requests": 0
  },
  "service_tier": "standard",
  "cache_creation": {
    "ephemeral_1h_input_tokens": 5396,
    "ephemeral_5m_input_tokens": 0
  },
  "inference_geo": "",
  "iterations": [],
  "speed": "standard"
}
```

---

### 3. `progress` -- Execution Progress

Progress entries track tool execution, hook runs, MCP calls, and subagent activity. They are the most frequent entry type.

#### Common Fields

| Field | Type | Description |
|-------|------|-------------|
| `data` | object | Progress-specific payload |
| `data.type` | string | Progress subtype |
| `toolUseID` | string | ID of the tool use this progress relates to |
| `parentToolUseID` | string | Parent tool use ID |

#### Progress Data Types

##### `hook_progress`

Fired when a hook (SessionStart, PreToolUse, PostToolUse, Stop) is executing:

```json
{
  "type": "progress",
  "data": {
    "type": "hook_progress",
    "hookEvent": "PostToolUse",
    "hookName": "PostToolUse:Bash",
    "command": "claude-code-sync hook PostToolUse"
  },
  "toolUseID": "toolu_01Vpck8FzT9N9zrtPRCm4MQN",
  "parentToolUseID": "toolu_01Vpck8FzT9N9zrtPRCm4MQN"
}
```

Hook events: `SessionStart`, `PreToolUse`, `PostToolUse`, `Stop`.

**Important for state detection:** `hook_progress` does NOT indicate the tool is actually executing -- it just means hooks are running. Only `bash_progress`, `mcp_progress`, and `agent_progress` indicate real tool execution.

##### `bash_progress`

Fired periodically while a Bash command is running:

```json
{
  "type": "progress",
  "data": {
    "type": "bash_progress",
    "output": "",
    "fullOutput": "",
    "elapsedTimeSeconds": 3,
    "totalLines": 0,
    "totalBytes": 0,
    "taskId": "b61qf3z5r",
    "timeoutMs": 60000
  },
  "toolUseID": "bash-progress-0",
  "parentToolUseID": "toolu_01GKNCWmnUmiotsg6CAz3Lzm"
}
```

Fields: `output`, `fullOutput`, `elapsedTimeSeconds`, `totalLines`, `totalBytes`, `taskId`, `timeoutMs`.

The `toolUseID` for bash progress follows the pattern `bash-progress-N` (incrementing).

##### `mcp_progress`

Fired when an MCP tool is starting/completing:

```json
{
  "type": "progress",
  "data": {
    "type": "mcp_progress",
    "status": "started",
    "serverName": "posthog",
    "toolName": "query-run"
  }
}
```

```json
{
  "data": {
    "type": "mcp_progress",
    "status": "failed",
    "serverName": "posthog",
    "toolName": "query-run",
    "elapsedTimeMs": 1004
  }
}
```

Statuses: `started`, `failed` (and presumably `completed`).

##### `agent_progress`

Fired to relay subagent (sidechain) messages to the main session. Contains a nested message from the subagent:

```json
{
  "type": "progress",
  "data": {
    "message": {
      "type": "user",
      "message": {
        "role": "user",
        "content": [{"type": "text", "text": "..."}]
      }
    }
  }
}
```

or

```json
{
  "data": {
    "message": {
      "type": "assistant",
      "timestamp": "2026-02-27T03:13:39.113Z",
      "message": {
        "model": "claude-haiku-4-5-20251001",
        "id": "msg_...",
        "role": "assistant",
        "content": [...]
      }
    }
  }
}
```

##### `waiting_for_task`

Fired when Claude Code is waiting on an async/background task:

```json
{
  "data": {
    "type": "waiting_for_task",
    "taskDescription": "Poll for require-all-checks-to-pass on PR #3502",
    "taskType": "local_bash"
  }
}
```

---

### 4. `system` -- System Events

System entries record metadata about the session lifecycle, errors, and compaction.

#### Common Fields

| Field | Type | Description |
|-------|------|-------------|
| `subtype` | string | Specific system event type |
| `level` | string | Log level: `"suggestion"`, `"info"`, or `"error"` |

#### Subtypes

##### `stop_hook_summary`

Emitted after a turn completes, summarizing all Stop hooks that ran:

```json
{
  "type": "system",
  "subtype": "stop_hook_summary",
  "hookCount": 3,
  "hookInfos": [
    {"command": "claude-code-sync hook Stop", "durationMs": 945},
    {"command": "/Users/nate/.local/bin/threader hook claude-code stop", "durationMs": 43},
    {"command": "${CLAUDE_PLUGIN_ROOT}/hooks/stop-hook.sh", "durationMs": 34}
  ],
  "hookErrors": [],
  "preventedContinuation": false,
  "stopReason": "",
  "hasOutput": false,
  "level": "suggestion",
  "toolUseID": "7b018268-dc11-45a5-928d-1e4c167395db"
}
```

##### `turn_duration`

Emitted after each turn completes, recording how long it took:

```json
{
  "type": "system",
  "subtype": "turn_duration",
  "durationMs": 35375,
  "isMeta": false
}
```

##### `compact_boundary`

Emitted when the conversation is compacted (context window management):

```json
{
  "type": "system",
  "subtype": "compact_boundary",
  "content": "Conversation compacted",
  "logicalParentUuid": "08f1a5de-c8e8-4046-8c34-4787362abd58",
  "parentUuid": null,
  "isMeta": false,
  "level": "info",
  "compactMetadata": {
    "trigger": "auto",
    "preTokens": 167072
  }
}
```

Key fields:
- `logicalParentUuid`: UUID of the last entry before compaction (logical chain)
- `parentUuid`: `null` (chain is broken by compaction)
- `compactMetadata.trigger`: `"auto"` or manual
- `compactMetadata.preTokens`: Token count before compaction

After a `compact_boundary`, the next `user` entry typically has `isCompactSummary: true` and contains a summary of the compacted conversation.

##### `api_error`

Emitted when an API call fails:

```json
{
  "type": "system",
  "subtype": "api_error",
  "level": "error",
  "error": {},
  "cause": {
    "code": "FailedToOpenSocket",
    "path": "https://api.anthropic.com/v1/messages?beta=true",
    "errno": 0
  },
  "retryInMs": 584.65,
  "retryAttempt": 1,
  "maxRetries": 10
}
```

##### `local_command`

Emitted when a local slash command is executed (e.g. `/mcp`):

```json
{
  "type": "system",
  "subtype": "local_command",
  "content": "<command-name>/mcp</command-name>\n<command-message>mcp</command-message>\n<command-args></command-args>",
  "level": "info"
}
```

---

### 5. `file-history-snapshot` -- File Backup Tracking

Tracks file backups for undo/restore functionality.

```json
{
  "type": "file-history-snapshot",
  "messageId": "24d9f71a-152d-44a5-97f3-6fbb239148b6",
  "snapshot": {
    "messageId": "24d9f71a-152d-44a5-97f3-6fbb239148b6",
    "trackedFileBackups": {
      "internal/tmux/feed.go": {
        "backupFileName": "e6e123bf2d905917@v1",
        "version": 1,
        "backupTime": "2026-02-28T22:51:47.077Z"
      }
    },
    "timestamp": "2026-02-28T22:51:36.600Z"
  },
  "isSnapshotUpdate": true
}
```

- `isSnapshotUpdate: false` -- initial snapshot at session start (usually empty `trackedFileBackups: {}`)
- `isSnapshotUpdate: true` -- update after a file was modified by a tool
- File paths in `trackedFileBackups` can be relative or absolute
- `backupFileName` can be `null` if no backup was created

---

### 6. `queue-operation` -- Agent Queue Events

Tracks task/agent queue operations in team/swarm scenarios:

**Enqueue:**
```json
{
  "type": "queue-operation",
  "operation": "enqueue",
  "timestamp": "2026-02-26T21:08:11.813Z",
  "sessionId": "16705cf4-...",
  "content": "<task-notification>\n<task-id>ac0aef8908a629430</task-id>\n<tool-use-id>toolu_012YEc72Q1k68jfbhRcZdCfT</tool-use-id>\n<status>completed</status>\n<summary>Agent completed</summary>\n..."
}
```

**Dequeue:**
```json
{
  "type": "queue-operation",
  "operation": "dequeue",
  "timestamp": "2026-02-26T21:08:11.871Z",
  "sessionId": "16705cf4-..."
}
```

---

### 7. `pr-link` -- GitHub PR Association

Links a session to a GitHub pull request:

```json
{
  "type": "pr-link",
  "sessionId": "16705cf4-...",
  "prNumber": 3554,
  "prUrl": "https://github.com/continuedev/remote-config-server/pull/3554",
  "prRepository": "continuedev/remote-config-server",
  "timestamp": "2026-02-26T21:25:34.224Z"
}
```

---

### 8. `custom-title` -- User-Defined Session Title

```json
{
  "type": "custom-title",
  "customTitle": "threader auth problems",
  "sessionId": "1768f0c1-..."
}
```

---

### 9. `agent-name` -- Named Agent Session

```json
{
  "type": "agent-name",
  "agentName": "Agent Sign Up",
  "sessionId": "d9370f7c-..."
}
```

---

## Session State Machine

The state of a Claude Code session is determined by walking all JSONL entries forward and tracking transitions. This is what the `att` tool does in `ClassifySessionState()`.

### States

| State | Description | Needs Attention? |
|-------|-------------|------------------|
| `Unknown` | No assistant entries yet, or only system/progress entries | No |
| `Working` | Claude is actively executing tools or generating a response | No |
| `Idle` | Claude finished its turn, waiting for user input | Yes |
| `Asking` | Claude invoked `AskUserQuestion`, waiting for user answer | Yes |
| `PlanMode` | Claude invoked `ExitPlanMode`, waiting for plan approval | Yes |
| `ToolPermission` | Claude wants to run a tool but is waiting for user approval | Yes |

### State Transitions

```
                                    +------- user entry -------> Working
                                    |
Unknown ---> assistant entry --+--> Idle (end_turn / stop_sequence / text-only null stop)
                               |
                               +--> Working (thinking-only null stop / empty content)
                               |
                               +--> ToolPermission (tool_use, any tool except Ask/Plan)
                               |     |
                               |     +-- bash_progress/mcp_progress/agent_progress --> Working
                               |     |
                               |     +-- user entry (tool result) --> Working
                               |
                               +--> Asking (tool_use with AskUserQuestion)
                               |
                               +--> PlanMode (tool_use with ExitPlanMode)

Working ---> assistant entry --> (same transitions as above)
Working ---> user entry ------> Working

Idle ------> user entry ------> Working

system/progress entries generally do NOT change state, except:
  - bash_progress/mcp_progress/agent_progress upgrade ToolPermission to Working
  - hook_progress does NOT change state
```

### Key Rules

1. **Sidechain entries are skipped** -- entries with `isSidechain: true` do not affect main session state.

2. **Streaming partials** -- assistant entries with `stop_reason: null` require inspecting content blocks:
   - Has `tool_use` blocks: classify by tool name (same as `stop_reason: "tool_use"`)
   - Has only `text` blocks: `Idle` (old version end-of-turn)
   - Has only `thinking` blocks or empty content: `Working` (streaming in progress)

3. **Hook progress vs tool progress** -- `hook_progress` entries fire for pre/post-tool hooks but do NOT indicate the tool is running. Only `bash_progress`, `mcp_progress`, and `agent_progress` confirm execution.

4. **Stale working detection** -- if a session is in `Working` state but the JSONL file hasn't been modified for >10 seconds, it likely means the tool is waiting for permission (Claude Code stopped writing progress entries). The `att` tool upgrades these to `ToolPermission`.

5. **User entries always reset to Working** -- any user entry with a `message` field (text input or tool result) means Claude will process it, so the state becomes `Working`.

### Typical Session Lifecycle

```
1. Session starts:
   progress (hook_progress, SessionStart)  -- hooks run at startup
   progress (hook_progress, SessionStart)  -- multiple hooks
   file-history-snapshot                   -- initial empty snapshot

2. User sends a message:
   user (content: "do something")          -- state -> Working

3. Claude thinks and responds:
   assistant (stop_reason: null, thinking)  -- state -> Working (streaming)
   assistant (stop_reason: null, text)      -- state -> Working (streaming)
   assistant (stop_reason: "tool_use")      -- state -> ToolPermission

4. Tool executes:
   progress (hook_progress, PostToolUse)    -- pre-tool hooks
   progress (bash_progress)                 -- state -> Working (tool running)
   progress (bash_progress)                 -- periodic updates
   file-history-snapshot (update)           -- if file was modified

5. Tool result returns:
   user (tool_result)                       -- state -> Working

6. Claude responds with text:
   assistant (stop_reason: "end_turn")      -- state -> Idle

7. Turn ends:
   progress (hook_progress, Stop)           -- stop hooks run
   system (stop_hook_summary)               -- hook summary
   system (turn_duration)                   -- timing info
```

---

## Subagent / Sidechain Files

Subagent JSONL files live in `{session-uuid}/subagents/agent-{id}.jsonl`. They have the same structure as main session files but with:

- `isSidechain: true` on all entries
- `agentId` field present
- Same entry types: `user`, `assistant`, `progress`, `system`
- No `file-history-snapshot`, `queue-operation`, `pr-link`, `custom-title`, or `agent-name` entries observed

Subagent activity is also mirrored into the main session file as `agent_progress` entries within `progress` objects.

Compact agent files (prefixed `agent-acompact-`) are compacted versions of longer subagent sessions.

---

## Compaction

When a session's context window fills up, Claude Code compacts the conversation:

1. A `system` entry with `subtype: "compact_boundary"` is emitted, with `compactMetadata` containing `trigger` (auto/manual) and `preTokens`
2. The `parentUuid` chain is broken (set to `null`)
3. `logicalParentUuid` preserves the logical link to the pre-compaction chain
4. A `user` entry with `isCompactSummary: true` follows, containing a summary of the compacted conversation
5. Normal operation resumes from this summary

---

## Edge Cases and Unusual States

### Old Claude Code Versions
- `stop_reason` may always be `null` -- state must be inferred from content blocks
- `slug` field may be absent

### Empty or Minimal Sessions
- A session file might contain only `progress` entries (SessionStart hooks) if the user never sent a message
- A session file might contain only `file-history-snapshot` entries

### Multiple Assistant Entries Per Request
- Same `message.id` and `requestId` across multiple lines
- Represents streaming: thinking -> text -> tool_use
- Only the last entry has the final `stop_reason`

### Parallel Tool Use
- Claude can request multiple tools in a single assistant entry
- Multiple `tool_use` blocks in `content` array
- Each gets its own `user` tool_result entry and progress entries

### Very Large Lines
- Tool results (especially file reads or bash output) can produce individual JSONL lines exceeding 256KB
- Parsers must handle arbitrary line lengths (no `bufio.Scanner` with default buffer)

### Sessions with Teams
- `teamName` field is present on entries
- `queue-operation` entries track task assignment
- Multiple sessions may coordinate via the team system

### API Errors and Retries
- `system` entries with `subtype: "api_error"` indicate transient failures
- Include `retryInMs`, `retryAttempt`, `maxRetries`
- The session typically recovers after retries

### PR Links
- A session can be linked to multiple PRs over its lifetime
- Each `pr-link` entry records the association

### Image Content
- User messages can include `image` blocks with base64-encoded image data
- `imagePasteIds` on the user entry tracks which images were pasted
- These can make individual JSONL lines very large
