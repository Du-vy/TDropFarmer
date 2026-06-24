# TDropFarmer Architecture Plan

## Project Intent

TDropFarmer is a new open-source Twitch drops and channel-points farmer written in Go.

This project should be designed as an independent implementation, not as a port of any existing Python project. The goal is a clean, maintainable, efficient CLI/Docker application with explicit boundaries between Twitch protocol handling, scheduling, domain state, storage, and user-facing configuration.

## Core Decisions

- Language: Go.
- Project name: TDropFarmer.
- Distribution: CLI and Docker first.
- Configuration format: JSON.
- License: permissive, preferably Apache-2.0 or MIT.
- Predictions: supported, but optional per global config and per streamer.
- Initial UI: none. No web panel in the first version.
- Implementation style: clean-room, independent implementation.

## Clean-Room Guidance

Because this project is intended to be published as open-source, implementation should avoid copying code, structure, naming, comments, or implementation details from GPL projects.

Acceptable sources of knowledge:

- Official Twitch OAuth documentation.
- Official Twitch API documentation where available.
- Independently observed Twitch client behavior.
- Self-authored experiments and protocol notes.
- Public protocol descriptions, if licensing permits reuse.

When protocol behavior is learned through observation, keep short self-authored notes in the repository, for example `PROTOCOL_NOTES.md`, with date, source type, and a summary written in this project's own words.

Avoid:

- Copying source code from existing miners.
- Copying module/class structure from existing miners.
- Copying persisted GraphQL operation collections verbatim from GPL code.
- Reusing comments or naming that strongly reflects another codebase.

The old Python miner may be used only as product-context reference for feature expectations. Its GPL-licensed code, operation definitions, comments, and internal structure must not be copied into this project.

The repository should include a short note explaining that TDropFarmer is an independent implementation.

## High-Level Architecture

```text
cmd/
  tdropfarmer/
    main.go

internal/
  app/
    app.go
    lifecycle.go

  config/
    config.go
    loader.go
    validate.go

  auth/
    device_flow.go
    session.go

  twitch/
    client.go
    gql/
      client.go
      operations.go
      errors.go
    realtime/
      client.go
      eventsub.go
      messages.go
      reconnect.go
    playback/
      hls.go
      presence.go
      minute_watched.go
    channelpoints/
      points.go
      claims.go
    inventory/
      drops.go
      campaigns.go
    predictions/
      predictions.go
      strategy.go

  engine/
    scheduler.go
    watcher.go
    priority.go
    streamer_worker.go
    events.go

  domain/
    streamer.go
    stream.go
    drop.go
    campaign.go
    prediction.go
    raid.go
    event.go

  store/
    store.go
    file_store.go
    token_store.go
    state_store.go

  notify/
    notifier.go
    discord.go
    telegram.go
    webhook.go

  logging/
    logger.go

  metrics/
    metrics.go
```

## Package Responsibilities

### cmd/tdropfarmer

CLI entrypoint.

Responsibilities:

- Parse flags.
- Load JSON config.
- Initialize logger.
- Create root context.
- Handle SIGINT/SIGTERM.
- Start `app.App`.

The entrypoint should stay thin. It should not contain Twitch logic.

### internal/app

Application composition and lifecycle.

Responsibilities:

- Wire dependencies.
- Initialize storage.
- Initialize Twitch clients.
- Run authentication flow when needed.
- Start real-time event transport and scheduler.
- Stop all components cleanly.

### internal/config

JSON config loading and validation.

Responsibilities:

- Load config from file.
- Apply defaults.
- Validate required fields.
- Normalize streamer login names.
- Validate safe prediction settings.

No package should read config files directly except `config`.

### internal/auth

OAuth/session handling.

Responsibilities:

- Twitch device-code OAuth flow.
- Token refresh for device-code tokens.
- Token validation.
- Central definition of requested OAuth scopes per enabled feature.
- Avoid storing user password.

The first implementation should use device-code login over username/password. It should not require a client secret. Token persistence belongs to `store`; `auth` should depend on a small token persistence interface instead of writing files directly.

The OAuth configuration must be explicit:

- `client_id` comes from a Twitch application controlled by the user/project, not from another miner.
- Requested scopes should be the minimum needed for enabled features.
- Each scope should be documented with the feature that requires it.
- Unsupported or undocumented Twitch behavior should be isolated outside the OAuth flow.

### internal/twitch/gql

GraphQL client abstraction.

Responsibilities:

- Send GraphQL requests.
- Attach auth/client headers.
- Decode errors.
- Apply HTTP timeouts.
- Retry transient failures with backoff.
- Keep operation definitions isolated.

This package should expose typed methods or low-level request helpers, but domain logic should live outside it.

### internal/twitch/realtime

Twitch real-time event transport.

Legacy Twitch PubSub should not be used as a foundation for this project. It has been deprecated for third-party integrations, so the runtime event layer should be transport-neutral and prefer supported EventSub WebSocket subscriptions where they fit. For viewer-specific behavior not exposed through official EventSub subscriptions, use documented APIs where possible, polling, or independently observed protocol behavior isolated behind this package.

Responsibilities:

- Connect to EventSub WebSocket where applicable.
- Create and renew supported EventSub subscriptions.
- Handle keepalive, reconnect, session reconnect, and revocation messages.
- Reconnect with backoff.
- Decode incoming messages into typed internal events.

The real-time client should emit events into `engine`, not mutate application state directly. Twitch-specific transport details must stay hidden behind a small interface so unsupported or unstable event sources can be replaced.

### internal/twitch/playback

Stream playback/presence handling.

Responsibilities:

- Obtain playback access tokens.
- Resolve HLS playlists.
- Select low-bandwidth stream target.
- Send presence/minute-watched style events.
- Hide fragile Twitch playback details behind a small interface.

This is likely one of the most fragile areas and should be easy to replace.

### internal/twitch/channelpoints

Channel points operations.

Responsibilities:

- Load channel points context.
- Track balance.
- Detect claimable bonus.
- Claim channel-points bonus.
- Emit point gain/spend events.

### internal/twitch/inventory

Drops and campaign operations.

Responsibilities:

- Load active campaigns.
- Load inventory progress.
- Match campaigns to streamers/games.
- Detect claimable drops.
- Claim drops.

### internal/twitch/predictions

Prediction operations and strategies.

Responsibilities:

- Track active predictions.
- Calculate prediction decision.
- Apply filters.
- Place predictions when enabled.
- Report win/loss/refund.

Prediction support must be optional globally and per streamer.

### internal/engine

Runtime orchestration.

Responsibilities:

- Maintain streamer runtime state.
- Choose which streamers to watch.
- Enforce max watched channels, default 2.
- React to real-time and polled Twitch events.
- Trigger enabled actions such as bonus claims, predictions, future drops, and future raids.
- Coordinate workers using context cancellation.

The engine should depend on interfaces, not concrete Twitch clients where practical.

### internal/domain

Pure domain models.

Responsibilities:

- Streamer.
- Stream.
- Drop.
- Campaign.
- Prediction.
- Raid.
- Events.

Domain objects should not perform HTTP requests or read files.

### internal/store

Local persistence.

Responsibilities:

- Token storage.
- Runtime state storage.
- Optional analytics data.
- Atomic file writes.

Initial implementation can use JSON files. A later version may add SQLite/BoltDB if useful. `store` owns token serialization and atomic persistence; `auth` owns token validity and refresh behavior.

### internal/notify

Notification integrations.

Responsibilities:

- Common notifier interface.
- Discord webhook.
- Telegram bot.
- Generic webhook.

Notifiers should be optional and failure should not stop farming.

### internal/logging

Structured logging setup.

Responsibilities:

- Text or JSON logs.
- Level control.
- Optional file output.

Use Go's standard `log/slog` unless a stronger reason appears.

### internal/metrics

Internal counters and optional future reporting.

Responsibilities:

- Points gained.
- Drops claimed.
- Predictions placed/won/lost/refunded.
- Online/offline transitions.
- Watch-loop status.

No web server in MVP, but metrics should be represented in a clean internal model.

## Runtime Flow

1. Parse CLI flags.
2. Load JSON config.
3. Validate config.
4. Initialize logger.
5. Initialize file store.
6. Load token/session.
7. If token is missing or invalid, run OAuth device-code flow.
8. Resolve configured streamers.
9. Load initial channel state and points context.
10. Start real-time event transport and subscribe to supported event sources.
11. Start scheduler.
12. Scheduler chooses up to `watch.max_channels` streamers to watch.
13. Playback client sends presence/minute-watched signals.
14. Real-time and polled events update engine state.
15. Engine triggers enabled actions such as bonus claims, predictions, and notifications.
16. State is persisted periodically and on shutdown.
17. SIGINT/SIGTERM cancels context and waits for graceful shutdown.

## MVP Scope

The first usable version should include:

- JSON config.
- CLI command.
- Docker image.
- OAuth device-code login.
- Token persistence.
- Streamer resolution by login.
- Online/offline detection.
- Real-time event connection and reconnection where supported.
- Channel points context.
- Automatic channel-points bonus claim.
- Watch scheduler with max 2 active channels by default.
- Basic presence/minute-watched behavior.
- Optional predictions.
- Structured logs.
- Dry-run mode.

Out of scope for MVP:

- Web UI.
- Analytics dashboard.
- Chat IRC.
- Drops campaign/inventory support.
- Auto-claim drops.
- Raid following.
- Advanced notification integrations beyond generic webhook or Discord.
- Multi-account orchestration.

## Post-MVP Scope

Potential second-phase features:

- Drops campaign/inventory support.
- Auto-claim drops.
- Raid following.
- Chat presence.
- Followers import and blacklist.
- Moments and community-goals support.
- Telegram notifications.
- Analytics export.
- Config hot reload.
- Multi-account support.
- Small optional web status page.

## Configuration Format

Initial `config.json` shape:

```json
{
  "account": {
    "username": "my_twitch_user"
  },
  "auth": {
    "client_id": "your_twitch_client_id",
    "scopes": []
  },
  "watch": {
    "max_channels": 2,
    "priorities": ["streak", "order"],
    "tick_seconds": 20
  },
  "features": {
    "claim_bonuses": true,
    "claim_drops": false,
    "follow_raids": false,
    "predictions": false,
    "dry_run": false
  },
  "predictions": {
    "strategy": "smart",
    "percentage": 5,
    "percentage_gap": 20,
    "max_points": 50000,
    "minimum_points": 20000,
    "delay_mode": "from_end",
    "delay_seconds": 6,
    "stealth_mode": false,
    "filter_condition": null
  },
  "streamers": [
    {
      "login": "streamer1",
      "predictions": false
    },
    {
      "login": "streamer2",
      "predictions": true,
      "prediction_settings": {
        "strategy": "most_voted",
        "percentage": 3,
        "percentage_gap": 20,
        "max_points": 10000
      }
    }
  ],
  "storage": {
    "path": "./data"
  },
  "logging": {
    "level": "info",
    "format": "text",
    "file": "./data/tdropfarmer.log"
  },
  "notifications": {
    "discord": {
      "enabled": false,
      "webhook_url": ""
    },
    "webhook": {
      "enabled": false,
      "url": "",
      "method": "POST"
    }
  }
}
```

In MVP, `claim_drops` and `follow_raids` should default to `false` because those features are post-MVP. Keeping the keys in the sample config is acceptable if validation rejects or warns when unsupported post-MVP features are enabled.

Per-streamer booleans are overrides. Missing means inherit the global value, `true` means force enabled for that streamer, and `false` means force disabled. In Go this should be represented with optional values such as `*bool`, not plain `bool`, so omitted and false remain distinct.

`auth.scopes` should be validated against enabled features. The sample keeps it empty because the exact scope set should be chosen from current Twitch documentation during implementation, not copied from another project.

## CLI Design

Suggested commands:

```text
tdropfarmer run --config ./config.json
tdropfarmer login --config ./config.json
tdropfarmer validate --config ./config.json
tdropfarmer version
```

Useful flags:

```text
--config path
--log-level debug|info|warn|error
--dry-run
--data-dir path
```

## Docker Design

Target behavior:

```sh
docker run --rm \
  -v ./config.json:/app/config.json:ro \
  -v ./data:/app/data \
  tdropfarmer:latest run --config /app/config.json
```

Docker image goals:

- Small final image.
- Non-root runtime user if practical.
- Static Go binary.
- Config mounted read-only.
- Data directory mounted writable.

## Concurrency Model

Use Go concurrency deliberately:

- One root `context.Context` for application lifecycle.
- One scheduler goroutine.
- One or more real-time transport goroutines, bounded by Twitch transport limits.
- HTTP calls with explicit per-request timeouts.
- Internal event channel from Twitch transports to engine.
- Bounded worker pool if concurrent HTTP work becomes necessary.
- No unbounded goroutine spawning.

Core internal event shape:

```go
type Event struct {
    Type      EventType
    Streamer  string
    ChannelID string
    Payload   any
    Time      time.Time
}
```

## Error Handling And Resilience

Required behavior:

- Retry transient HTTP errors with backoff.
- Reconnect WebSockets automatically.
- Detect invalid token and ask for login again.
- Do not crash on notification failures.
- Persist state with atomic file writes.
- Log enough context to debug Twitch protocol changes.
- Provide `dry_run` to test decisions without performing claims/predictions.

## Prediction Safety

Predictions are supported but must be explicitly configurable.

Rules:

- Global predictions default should be false.
- Per-streamer predictions can override global behavior.
- Minimum points should be supported.
- Maximum points should be supported.
- Percentage-based stake should be supported.
- Percentage gap should be supported for `smart` style decisions.
- Optional filters should support `by`, `where`, and `value` fields.
- Dry-run must log prediction decisions without placing them.
- Prediction logic should be unit-tested.

Initial strategies:

- `most_voted`.
- `high_odds`.
- `percentage`.
- `smart_money`.
- `smart`.
- `fixed_outcome_1` through `fixed_outcome_8`.

Initial filter keys:

- `percentage_users`.
- `odds_percentage`.
- `odds`.
- `decision_users`.
- `decision_points`.
- `top_points`.
- `total_users`.
- `total_points`.

Initial filter operators:

- `gt`.
- `gte`.
- `lt`.
- `lte`.

## Testing Plan

Unit tests:

- Config defaults and validation.
- Priority scheduler.
- Prediction strategy calculations.
- Real-time event message parsing.
- GraphQL error parsing.
- Atomic file store writes.

Integration-style tests:

- Mock GraphQL server.
- Mock EventSub WebSocket server.
- Login/token store with temporary directory.

Manual smoke tests:

- `tdropfarmer validate`.
- `tdropfarmer login`.
- `tdropfarmer run --dry-run`.
- Docker run with mounted config/data.

## Security And Privacy

- Never require storing a Twitch password.
- Token files should be stored under the configured data directory.
- Token files should not be logged.
- Config examples must not contain real tokens/webhooks.
- `.gitignore` should exclude data, tokens, logs, and local configs if needed.

## Legal And Terms Risk

TDropFarmer will interact with Twitch behavior that may include undocumented or unstable endpoints. This carries risk:

- Twitch can change endpoints or protocols.
- Features may stop working without warning.
- Automation may conflict with Twitch terms or expectations.
- Users should understand account risk.

The README should include a clear disclaimer.

## Initial Implementation Milestones

### Milestone 1: Project Skeleton

- Create Go module.
- Add CLI entrypoint.
- Add config loading/validation.
- Add structured logging.
- Add Dockerfile.
- Add README and license.

### Milestone 2: Auth And Storage

- Implement device-code login.
- Persist token/session.
- Validate token.
- Add `login` command.

### Milestone 3: Twitch Client Basics

- Implement base HTTP client.
- Implement GraphQL request wrapper.
- Resolve user/channel IDs.
- Load initial streamer context.

### Milestone 4: Real-Time Events

- Implement EventSub WebSocket client where supported.
- Create and maintain supported subscriptions.
- Add polling or isolated observed-protocol adapters for viewer-specific gaps.
- Decode basic messages into internal events.
- Reconnect on failures.

### Milestone 5: Scheduler And Watching

- Implement priority scheduler.
- Enforce max watched channels.
- Implement basic playback/presence loop.
- Track online/offline state.

### Milestone 6: Claims

- Detect available channel-points bonus.
- Claim bonus unless dry-run is enabled.
- Log and persist point gains.

### Milestone 7: Predictions

- Parse prediction events.
- Implement strategies.
- Implement filters and delays.
- Place predictions when enabled.
- Fully support dry-run.

### Milestone 8: Drops And Raids

- Load campaigns/inventory.
- Track drop progress.
- Claim drops.
- Follow raids if enabled.

## Open Questions

- Choose Apache-2.0 or MIT.
- Decide whether post-MVP feature keys should remain in the default sample config or move to an extended example.
- Decide whether local config should be committed as `config.example.json` only.
- Decide whether token/session format should be plain JSON or encrypted where supported by OS.
- Decide whether first release should support only one account.
