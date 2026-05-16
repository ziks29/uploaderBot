# Fivemanage Uploader Bot

<p align="center">
  <img src="logo_v1.png" alt="Fivemanage Uploader Bot" width="200"/>
</p>

<p align="center">
  <img src="https://img.shields.io/github/v/release/ziks29/uploaderBot?label=version" alt="Version"/>
  <img src="https://img.shields.io/badge/go-1.23-00ADD8?logo=go" alt="Go Version"/>
  <img src="https://img.shields.io/badge/discord-bot-5865F2?logo=discord&logoColor=white" alt="Discord"/>
</p>

A Discord bot (User-Installable App) that uploads files to [Fivemanage](https://fivemanage.com) CDN. Each user connects their own free Fivemanage account — files go to their storage, under their responsibility. Supports English and Russian.

## Use without self-hosting

Join the official Discord server and the bot is ready to use — no setup needed on your end:

**[discord.gg/E2wSb2QEz8](https://discord.gg/E2wSb2QEz8)**

Once in the server, open a DM with the bot and follow the prompts to connect your Fivemanage account.

## Features

- Upload images, videos, and other files via Discord DMs
- Upload from URLs pasted in chat
- Per-user API key management with AES-256 encryption
- Delete uploaded files directly from Discord
- English and Russian localization
- Slash commands with localized descriptions

## How it works

1. User creates a free account at [fivemanage.com](https://fivemanage.com)
2. User generates a **Media** API key in their [dashboard](https://app.fivemanage.com)
3. User runs `/setkey` or clicks **Enter API Key** in the bot DMs
4. User sends any file to the bot in DMs → bot uploads it and replies with the CDN link

---

## Quick Start

### Option A — Docker (recommended)

**Prerequisites:** Docker and Docker Compose installed.

```bash
# 1. Clone the repository
git clone https://github.com/ziks29/uploaderBot.git
cd uploaderBot

# 2. Configure environment
cp .env.example .env
# Edit .env with your Discord token, Application ID, and Encryption Key

# 3. Create the data directory for persistence
mkdir -p data

# 4. Start the bot
docker compose up -d
```

To generate a secure encryption key:
```bash
openssl rand -hex 32
```

### Option B — Pre-built Binary

Download the latest binary for your platform from the [Releases](https://github.com/ziks29/uploaderBot/releases/latest) page:

| Platform | File |
|---|---|
| Linux (x64) | `uploaderBot-linux-amd64` |
| Linux (ARM64) | `uploaderBot-linux-arm64` |
| Windows (x64) | `uploaderBot-windows-amd64.exe` |
| macOS (Intel) | `uploaderBot-darwin-amd64` |
| macOS (Apple Silicon) | `uploaderBot-darwin-arm64` |

```bash
# Linux / macOS
chmod +x uploaderBot-linux-amd64
cp .env.example .env  # fill in your values
./uploaderBot-linux-amd64

# Windows — double-click uploaderBot-windows-amd64.exe
# or run from PowerShell after setting up .env
```

### Option C — Build from Source

**Prerequisites:** Go 1.23+

```bash
git clone https://github.com/ziks29/uploaderBot.git
cd uploaderBot
cp .env.example .env  # fill in your values
go build -ldflags="-s -w" -o uploaderBot .
./uploaderBot
```

---

## Discord Application Setup

1. Go to the [Discord Developer Portal](https://discord.com/developers/applications) and click **New Application**
2. **Installation** tab:
   - Under **Installation Contexts**, enable **User Install**
   - Under **Default Install Settings → User Install**, add scope: `applications.commands`
3. **Bot** tab:
   - Click **Reset Token** and copy it → `DISCORD_TOKEN`
   - Enable **Server Members Intent** under Privileged Gateway Intents
4. **General Information** tab: copy the **Application ID** → `APPLICATION_ID`
5. **Installation** tab → **Install Link** → open in browser → choose **Install to User**

---

## Commands

| Command | Description |
|---|---|
| `/setkey` | Save your Fivemanage API key (validated on save) |
| `/removekey` | Remove your stored API key |
| `/status` | Check if your key is configured |
| `/language` | Change the bot language (English / Русский) |
| `/help` | Show usage instructions |
| `/start` | Show the welcome message |

---

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `DISCORD_TOKEN` | Yes | Your Discord bot token |
| `APPLICATION_ID` | Yes | Your Discord application ID |
| `ENCRYPTION_KEY` | Recommended | 32-byte hex key for AES-256 encryption of stored API keys |
| `DISCORD_GUILD_ID` | No | Restrict slash commands to one guild (instant registration, useful for testing) |
| `KEYS_FILE` | No | Path to the user data file (default: `keys.json`) |

---

## Security

- All slash command responses with sensitive data are **ephemeral** (only visible to the user)
- API keys are encrypted at rest using **AES-256-GCM** when `ENCRYPTION_KEY` is set
- Keys that were stored in plain text are automatically migrated to encrypted format on startup
- Only the uploader can delete their own files
- The bot only processes messages in **Direct Messages** — it ignores all server messages

---

## Updating

### Docker
```bash
docker compose pull
docker compose up -d
```

### Binary
Download the new binary from the [Releases](https://github.com/ziks29/uploaderBot/releases/latest) page and replace the existing one. Your `keys.json` / `data/` directory is preserved.

---

## Self-Hosting Notes

- `keys.json` (or `data/keys.json` in Docker) stores all user API keys — **back this file up** and keep it private
- If you lose `ENCRYPTION_KEY`, all stored API keys become unreadable and users will need to re-enter them
- For production, run the binary as a **non-root system service** (systemd, Docker with a non-root user, etc.)
- Global slash command registration can take **up to 1 hour** to propagate. Use `DISCORD_GUILD_ID` during testing for instant updates
