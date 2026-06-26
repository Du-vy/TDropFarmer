# TDropFarmer 🎁💰

TDropFarmer is a lightweight, clean-room Twitch Drops and Channel Points farmer bot written in Go. It operates efficiently in the background, automatically monitoring active drop campaigns, switching streams to maximize progress, and claiming channel points and drop rewards.

Designed for performance and safety, it runs as a native CLI binary or within a minimal Docker container.

---

## Features

- **Automated Drops Farming**: Dynamically tracks active campaigns, watches the most relevant live streams, and advances progress.
- **Channel Points Claiming**: Automatically claims periodic channel point bonuses (bonuses, click-to-claims) for watched streams.
- **Priority-Based Farming**: Configure a custom list of games to prioritize. If no campaigns are available for those games, it falls back to others.
- **Device Authentication Flow**: Log in securely using Twitch's official device activation flow.
- **Discord & Webhook Notifications**: Receive notifications on Discord or custom endpoints when drops are claimed or progress updates.
- **Clean Logging**: Beautifully formatted terminal logs with exact drop percentages, points updates, and streamer status.
- **Ultra-lightweight Docker Image**: Built on top of Alpine Linux (minimal footprint, low resource consumption).

---

## Quick Start (with Docker & Docker Compose)

The easiest way to run TDropFarmer is via Docker Compose.

### Prerequisites
- [Docker](https://docs.docker.com/get-docker/)
- [Docker Compose](https://docs.docker.com/compose/install/)

### Setup

1. Copy the example configuration file:
   ```sh
   cp config.example.json config.json
   ```
2. Edit `config.json` and configure your Twitch username:
   ```json
   "account": {
     "username": "your_twitch_username"
   }
   ```
3. Start the application:
   ```sh
   docker-compose up -d
   ```
4. Authenticate your account:
   Look at the container logs to get the Twitch activation code:
   ```sh
   docker-compose logs tdropfarmer
   ```
   Open [twitch.tv/activate](https://twitch.tv/activate), enter the code shown in the logs, and authorize the application.

---

## Installation & Running (Local Go Binary)

If you prefer to run TDropFarmer locally:

### Build from Source
Ensure you have **Go 1.26+** installed.

```sh
# Clone the repository
git clone https://github.com/Du-vy/TDropFarmer.git
cd TDropFarmer

# Build the binary
go build -o tdropfarmer ./cmd/tdropfarmer
```

### Commands

* **Run the bot**:
  ```sh
  ./tdropfarmer run --config ./config.json
  ```
* **Verify configuration**:
  ```sh
  ./tdropfarmer validate --config ./config.json
  ```
* **Print version**:
  ```sh
  ./tdropfarmer version
  ```

---

## Configuration Guide (`config.json`)

Here is an overview of the key configuration fields:

| Field | Type | Description |
| :--- | :--- | :--- |
| `account.username` | String | Your Twitch login username. |
| `watch.priority_games` | Array | List of games to prioritize (e.g. `["World of Tanks: HEAT", "Corepunk"]`). Matches Twitch categories exactly. |
| `watch.fallback_all_campaigns` | Boolean | If `true`, the bot will farm any available drop campaigns if none of your priority games are live. |
| `watch.auto_start_campaigns` | Boolean | Automatically start available campaigns if not already in progress. |
| `watch.tick_seconds` | Integer | Interval in seconds between engine evaluation loops (default: `20`). |
| `features.claim_bonuses` | Boolean | Enable automated channel points bonus claims. |
| `features.claim_drops` | Boolean | Enable automated drop reward claiming. |
| `features.dry_run` | Boolean | If `true`, simulates claiming rewards without performing the actual claim requests. |
| `storage.path` | String | Path to store local data, authorization tokens, and caches (default: `./data`). |
| `logging.level` | String | Logging level (`debug`, `info`, `warn`, `error`). |
| `notifications` | Object | Discord / Custom Webhook integration settings. |

---

## Clean-Room Implementation Note

TDropFarmer is developed as an independent clean-room implementation. It does not copy source code, internal structures, comments, or GraphQL collections from GPL-licensed Twitch miners. Instead, it relies on official Twitch developer specifications, self-documented protocols, and independently observed client-side telemetry endpoints.

---

## Disclaimer

This software is not affiliated, associated, authorized, endorsed by, or in any way officially connected with Twitch Interactive, Inc. Use of automated bots carries risks of account suspension. Use at your own risk.

---

## License

This project is licensed under the [MIT License](LICENSE).
