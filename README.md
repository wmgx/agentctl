# agentctl

[中文文档](./README_zh.md)

A Go service that connects AI CLI tools (like Claude Code) to messaging channels and notification pipelines. Currently supports Feishu (Lark) as the IM channel, with plans to expand to other channels and CLI tools.

## Features

- **Multi-channel architecture** — IM bots, CLI tools, and notification channels as first-class citizens
- **Feishu integration** — WebSocket long-polling (no public domain required), interactive cards, group session management
- **AI CLI bridge** — Drives AI CLI tools (Claude Code, etc.) as subprocesses with streaming output
- **Intent classification** — Natural language routing, no commands to memorize
- **Session isolation** — Each conversation gets its own tmux session and working directory
- **Reply chain upgrade** — Automatically escalates P2P chats to group sessions for richer context
- **Cron scheduling** — Periodic tasks with configurable triggers

## Architecture

```
Feishu / CLI / Notifications
         │
    ┌────▼─────┐
    │ agentctl │  ← Go service (single process)
    │          │
    │  Router  │  ← Intent classification & routing
    │  Session │  ← Session lifecycle management
    │  Adapter │  ← AI CLI subprocess (streaming)
    └──────────┘
         │
    AI CLI Tool (e.g. Claude Code)
```

## Requirements

- Go 1.20+
- [Claude Code CLI](https://claude.ai/claude-code) (or any compatible AI CLI tool)
- tmux
- A Feishu app with the following permissions:
  - `im:message`
  - `im:message:send_as_bot`
  - `im:chat`
  - `im:chat:create`

## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/wmgx/agentctl/main/install.sh | bash
```

Or manually:

```bash
git clone https://github.com/wmgx/agentctl.git
cd agentctl
go build -o agentctl ./cmd/server
```

## Configuration

Copy the example config and fill in your credentials:

```bash
cp config.example.json config.json
```

```json
{
  "feishu": {
    "app_id": "cli_xxxx",
    "app_secret": "xxxx",
    "bot_name": "YourBotName"
  },
  "anthropic": {
    "api_key": "sk-ant-xxxx",
    "model": "claude-haiku-4-5-20250929",
    "base_url": "",
    "auth_token": ""
  },
  "default_cwd": "/path/to/your/workspace",
  "repos": {
    "my-service": "/path/to/my-service"
  },
  "idle_timeout_min": 30,
  "dangerous_tools": ["rm ", "git push", "git reset"],
  "claude_cli_path": "claude"
}
```

## Update

```bash
bash install.sh update
```

Fetches the latest git tag, rebuilds, replaces the binary, and restarts the service. Your `config.json` is preserved.

## Service Management

`install.sh` automatically registers a system daemon on install:

| Platform | Backend | How to check |
|----------|---------|--------------|
| macOS | launchd | `launchctl list \| grep agentctl` |
| Linux | systemd (user) | `systemctl --user status agentctl` |
| Windows | ❌ not supported | Use WSL2 instead |

Logs are written to `~/.agentctl/log/`.

## Running

```bash
./agentctl
```

The service connects to Feishu via WebSocket and starts listening for messages.

## Usage

Send a message to your Feishu bot to start a session. The bot will:

1. Classify your intent automatically
2. Create a group session if needed (reply chain upgrade)
3. Forward your request to the AI CLI tool
4. Stream output back to Feishu as an interactive card

Example interactions:

```
"帮我看一下 order-service 的日志"
"重构 user.go，把 SQL 抽到 repository 层"
"最近的 5 个 commit 有没有问题"
```

## Project Structure

```
agentctl/
├── cmd/server/        # Entry point
├── internal/
│   ├── claude/        # AI CLI adapter & tmux session management
│   ├── config/        # Config loading
│   ├── cron/          # Scheduled tasks
│   ├── feishu/        # Feishu client, event handling, cards
│   ├── intent/        # Intent classification
│   ├── router/        # Message routing
│   └── session/       # Session lifecycle
├── config.example.json
└── install.sh
```

## License

MIT
