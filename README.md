# Universal Telegram MTProto Session Generator

[![Go Version](https://img.shields.io/badge/Go-1.24-00ADD8?style=flat&logo=go)](https://golang.org)
[![Python Version](https://img.shields.io/badge/Python-3.12%2B-3776AB?style=flat&logo=python)](https://python.org)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?style=flat&logo=docker)](https://docker.com)
[![Render Blueprint](https://img.shields.io/badge/Render-Blueprint-46E3B7?style=flat&logo=render)](https://render.com)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A high-performance, security-focused Telegram Bot that generates native **MTProto String Sessions** across three major Telegram libraries: **Pyrogram** (powered by Pyrofork), **Telethon**, and **Gogram**.

> **Core Principle:** One Telegram Bot. Three MTProto Libraries. One Unified Session Generation Interface.

---

## Table of Contents

- [Overview](#overview)
- [Key Features](#key-features)
- [Supported Providers & Native Session Formats](#supported-providers--native-session-formats)
- [Architecture & Design](#architecture--design)
- [Strict Security & Data Privacy Policy](#strict-security--data-privacy-policy)
- [Environment Configuration](#environment-configuration)
- [Local Development](#local-development)
- [Docker Deployment](#docker-deployment)
- [Deploying on Render](#deploying-on-render)
- [Broadcasting System (Owner Only)](#broadcasting-system-owner-only)
- [Testing](#testing)
- [Troubleshooting](#troubleshooting)
- [License](#license)

---

## Overview

The Universal Telegram Session Generator solves the fragmentation problem among Telegram bot developers and userbot operators. Different applications require different MTProto string sessions (e.g. Pyrogram apps, Telethon Python scripts, and Gogram Go bots). 

This bot orchestrates all three providers through an intuitive Telegram interface with private-chat session delivery, automated fresh-client validation, and zero permanent credential storage.

---

## Key Features

- **Multi-Provider Support:**
  - **Pyrogram:** Generates native Pyrogram/Pyrofork v2 String Sessions (powered by an isolated Pyrofork worker engine).
  - **Telethon:** Generates native Telethon v1 String Sessions.
  - **Gogram:** Generates native Base64 Gogram String Sessions.
- **Unified Telegram Interface:** Clean navigation powered by `ReplyKeyboardMarkup` (`/start`, `Generate Session`, `Help`, `About`, `Cancel`).
- **Flexible Authentication Workflows:**
  - Phone Number + OTP verification (supporting Telegram App & SMS codes).
  - Two-Step Verification (2FA cloud password) handling.
  - Interactive QR Code Login (with rendered QR images for providers that genuinely support it).
- **Mandatory Session Validation:** Every exported session is validated by connecting a temporary, fresh client before private delivery.
- **Strict Ephemeral State & Zero Disk Credentials:**
  - All temporary auth states and worker processes run exclusively in memory.
  - Instant cleanup after session delivery, user cancellation, or inactivity timeouts.
- **Owner Broadcasting System:**
  - Protected by `OWNER_ID`.
  - Supports text, photos, videos, and documents with captions.
  - Real-time progress updates, Telegram FloodWait backoff, and completion statistics.
- **MongoDB Minimal Storage:** Permanently persists *strictly and exclusively* numeric Telegram User IDs (`user_id`) for broadcasting.
- **Production-Ready Multi-Stage Dockerfile & Render Blueprint (`render.yaml`).**

---

## Supported Providers & Native Session Formats

Each library uses its own native serialization format. Sessions are **never** converted or assumed interchangeable:

| Provider | Core Technology | Execution Mode | Native Session Format | QR Login Support |
| :--- | :--- | :--- | :--- | :---: |
| **Pyrogram** | Python 3 (`pyrofork` engine) | Isolated Python worker process | Base64-encoded Pyrogram Session String | ❌ *(Phone only)* |
| **Telethon** | Python 3 (`telethon`) | Isolated Python worker process | Base64-encoded Telethon `StringSession` | ✅ *(Supported)* |
| **Gogram** | Go (`amarnathcjd/gogram`) | Native Go MTProto client | Base64-encoded Gogram Session String | ✅ *(Supported)* |

> **Note on Pyrogram Implementation:** To guarantee long-term stability and compatibility with current Telegram MTProto schema updates, the Pyrogram provider is powered internally by the actively maintained [Pyrofork](https://github.com/Mayuri-Chan/pyrofork) engine. The resulting session strings are 100% compatible with both Pyrogram and Pyrofork applications.

---

## Architecture & Design

```
+--------------------------------------------------------------------------+
|                        Go Application (Host)                             |
|                                                                          |
|  +---------------------+   +--------------------+   +-----------------+  |
|  | Telegram Bot API    |   | State Machine      |   | Provider        |  |
|  | (gotgbot/v2)        |-->| (In-memory/TTL)    |-->| Registry        |  |
|  +---------------------+   +--------------------+   +-----------------+  |
|                                                              |           |
|                                     +------------------------+           |
|                                     |                        |           |
|                                     v                        v           |
|                            +-----------------+      +-----------------+  |
|                            | Native Go       |      | Python Worker   |  |
|                            | (Gogram MTProto)|      | Manager (IPC)   |  |
|                            +-----------------+      +-----------------+  |
+--------------------------------------------------------------|-----------+
                                                               | JSON Lines
                                                   stdin/stdout| over Pipes
                                                               v
                                    +-------------------------------------+
                                    | Isolated Subprocess Worker          |
                                    | (pyrofork/worker.py or              |
                                    |  telethon/worker.py)                |
                                    +-------------------------------------+
```

### Python Worker Isolation
- Pyrogram (powered by Pyrofork) and Telethon run inside dedicated Python 3 worker processes.
- Communication with Go occurs over standard input/output using a structured JSON protocol (`workers.Request` / `workers.Response`).
- **No passwords or OTP codes are ever passed via command-line arguments.**
- Dedicated process lifecycles with hard timeouts guarantee child workers are terminated upon completion or cancellation.

---

## Strict Security & Data Privacy Policy

The application enforces strict privacy constraints:

### 1. Allowed Permanent Data (MongoDB)
The database collection strictly stores only:
```json
{
  "_id": ObjectId("..."),
  "user_id": 123456789
}
```
A unique index on `user_id` prevents duplicate registrations.

### 2. Prohibited Data (Never Stored or Logged)
- ❌ Phone numbers
- ❌ OTP login codes
- ❌ 2FA passwords
- ❌ String sessions
- ❌ Telegram auth keys / tokens
- ❌ Usernames, first names, or profile details

### 3. Automatic Log Redaction
Custom `slog.Handler` filters automatically scrub phone numbers, OTP codes, 2FA passwords, bot tokens, and session strings before writing to stdout.

---

## Environment Configuration

Configure the following environment variables (via `.env` locally or the Render Dashboard in production):

| Variable | Required | Description | Example |
| :--- | :---: | :--- | :--- |
| `BOT_TOKEN` | **Yes** | Telegram Bot API Token from [@BotFather](https://t.me/BotFather) | `123456789:ABCdefGHI...` |
| `API_ID` | **Yes** | Telegram App API ID from [my.telegram.org](https://my.telegram.org) | `1234567` |
| `API_HASH` | **Yes** | Telegram App API Hash from [my.telegram.org](https://my.telegram.org) | `0123456789abcdef...` |
| `MONGODB_URI` | **Yes** | MongoDB connection string (Atlas or self-hosted) | `mongodb+srv://user:pass@cluster.mongodb.net/` |
| `MONGODB_DATABASE`| No | MongoDB Database Name (defaults to `telegram_session_bot`) | `session_generator` |
| `OWNER_ID` | **Yes** | Numeric Telegram User ID of bot administrator | `123456789` |
| `LOG_LEVEL` | No | Logging verbosity: `DEBUG`, `INFO`, `WARN`, `ERROR` | `INFO` |
| `PYTHON_PATH` | No | Path to Python interpreter (auto-detected by default) | `python3` or `/usr/local/bin/python3` |
| `WORKER_TIMEOUT_SECONDS` | No | Worker command timeout in seconds (default: `300`) | `300` |

---

## Local Development

### Prerequisites
- Go 1.23+ or Go 1.24+
- Python 3.10+
- MongoDB instance (local or Atlas)

### Setup Steps

1. **Clone the repository:**
   ```bash
   git clone https://github.com/universal-session-generator.git
   cd universal-session-generator
   ```

2. **Install Python Worker Dependencies:**
   ```bash
   pip install -r workers/pyrofork/requirements.txt -r workers/telethon/requirements.txt
   ```

3. **Install Go Modules:**
   ```bash
   go mod download
   ```

4. **Create your environment configuration:**
   ```bash
   cp .env.example .env
   # Edit .env with your real credentials
   ```

5. **Run the bot:**
   ```bash
   go run ./cmd/bot
   ```

---

## Docker Deployment

The application features a production-ready, multi-stage Dockerfile that builds the Go binary and provides an isolated Python runtime with non-root security.

### Build and Run with Docker Compose

1. **Configure `.env`** with your credentials.
2. **Start the services:**
   ```bash
   docker compose up -d --build
   ```
3. **Inspect logs:**
   ```bash
   docker compose logs -f bot
   ```
4. **Stop the services:**
   ```bash
   docker compose down
   ```

---

## Deploying on Render

Render is the primary production target. The bot supports both **Render Web Service** (including the **Free tier**) and **Render Background Worker** using **Docker runtime** and Telegram **long polling**.

The application includes an internal HTTP health check server that listens on `$PORT` (default `:10000` or `:8080`) providing `/healthz` and `/` endpoints. This ensures Render's port-scanning probes pass immediately, preventing port scan timeouts.

### Step-by-Step Blueprint Deployment

1. **Push your repository to GitHub.**
2. Log into the [Render Dashboard](https://dashboard.render.com).
3. Click **New +** and select **Blueprint**.
4. Connect your GitHub repository.
5. Render will automatically detect and parse [`render.yaml`](render.yaml):
   - Service Type: `Web Service`
   - Runtime: `Docker`
   - Plan: `Free`
   - Health Check Path: `/healthz`
6. Under **Environment Variables**, fill in your secret credentials:
   - `BOT_TOKEN`: Your token from @BotFather.
   - `API_ID`: Your API ID from my.telegram.org.
   - `API_HASH`: Your API Hash from my.telegram.org.
   - `MONGODB_URI`: Your MongoDB connection string (e.g., MongoDB Atlas URI).
   - `MONGODB_DATABASE`: (e.g. `telegram_session_bot`).
   - `OWNER_ID`: Your Telegram numeric User ID.
7. Click **Apply Blueprint**.
8. Wait for Render to build the multi-stage Docker container and deploy the service.
9. Open the **Logs** tab in Render:
   - Verify: `Health check HTTP server listening port=10000`
   - Verify: `MongoDB unique index on user_id verified`
   - Verify: `Bot long-polling started. Listening for updates...`
10. Open Telegram, open your bot, and send `/start`.

### Deploying Manually via Render Dashboard

If you prefer deploying manually without Blueprints:
1. Click **New +** > **Web Service**.
2. Connect your GitHub repository.
3. Select **Docker** as the Environment / Runtime.
4. Set **Health Check Path** to `/healthz`.
5. Under **Environment Variables**, add:
   - `BOT_TOKEN`, `API_ID`, `API_HASH`, `MONGODB_URI`, `MONGODB_DATABASE`, `OWNER_ID`
   - `PORT`: `10000`
6. Click **Create Web Service**.

### Updating After GitHub Pushes
- With `autoDeploy: true` configured in `render.yaml`, any push to your connected branch automatically triggers a zero-downtime rebuild and restart.
- Alternatively, click **Manual Deploy > Clear build cache & deploy** in the Render Dashboard.

### Diagnosing Common Issues on Render
- **Port Scan Timeout:** The Go server automatically binds to `$PORT` and exposes `/healthz` and `/` endpoints. Ensure the Health Check Path is set to `/healthz` or `/`.
- **Telegram 409 Conflict:** Telegram allows only one polling instance per bot token. The bot automatically deletes stale webhooks and clears pending updates on boot. When redeploying on Render, the port health check ensures rapid container handover so previous instances are promptly shut down.
- **MongoDB Connection Timeout:** Ensure your MongoDB Atlas Network Access whitelist allows connections from all IPs (`0.0.0.0/0`), as Render containers use dynamic egress IPs.
- **Worker Process Failures:** Stderr output from Python workers is streamed directly to Render logs with sensitive values redacted.

---

## Broadcasting System (Owner Only)

The bot includes a high-capacity broadcasting engine restricted to the configured `OWNER_ID`.

### Commands
- `/broadcast`: Initiates the broadcast creation flow.
  1. Send any message: pure text, photo, video, or document with formatting and captions.
  2. The bot previews the content and displays confirmation buttons.
  3. Click `✅ Confirm Broadcast`.
  4. Real-time progress updates are displayed to the owner.
  5. Upon completion, a full report is delivered.
- `/stats`: Displays the total number of registered users stored in MongoDB.
- `/cancel`: Cancels any active or pending broadcast immediately.

---

## Testing

Comprehensive unit and integration test suites are provided.

### Run All Go Tests
```bash
go test -v ./...
```

### Run Python Worker Tests
```bash
python tests/test_workers.py
```

### Test Coverage Summary
- State machine transition validation (`internal/auth/state_test.go`)
- Session expiry and reaper routines
- Credential scrubbing and slog redaction (`internal/security/redaction_test.go`)
- Token bucket rate limiter (`internal/security/ratelimit_test.go`)
- MTProto provider registry and normalizer (`internal/providers/registry_test.go`)
- Owner broadcast authorization and lifecycle (`internal/broadcast/manager_test.go`)
- IPC request/response serialization (`internal/workers/protocol_test.go`)
- Live Go-to-Python IPC worker communication (`tests/integration_test.go`)
- Python worker action dispatching (`tests/test_workers.py`)

---

## Troubleshooting

| Problem | Cause | Solution |
| :--- | :--- | :--- |
| `missing required environment variable: ...` | Environment variable not set | Verify that `.env` is loaded locally, or check the Environment tab on Render. |
| `PHONE_NUMBER_INVALID` | Phone number format is incorrect | Provide international format starting with `+` (e.g. `+1234567890`). |
| `PHONE_CODE_EXPIRED` | OTP verification took too long | Re-initiate authentication and enter the code promptly. |
| `SESSION_PASSWORD_NEEDED` | Account has Two-Step Verification | Enter your Telegram cloud password when prompted. |
| `FloodWait error` | Telegram rate limit triggered on account | Wait the specified duration before attempting to authenticate again. |
| QR Code expired | QR login timed out after 3 minutes | Re-select QR Code login to generate a fresh token. |

---

## License

This project is licensed under the [MIT License](LICENSE).
