# TDropFarmer

TDropFarmer is a clean-room Twitch drops and channel-points farmer written in Go.

This repository is in early implementation. The current code provides the project skeleton, JSON configuration loading and validation, structured logging, and CLI commands.

## Commands

```sh
tdropfarmer run --config ./config.json
tdropfarmer login --config ./config.json
tdropfarmer validate --config ./config.json
tdropfarmer version
```

## Development

```sh
go test ./...
go run ./cmd/tdropfarmer validate --config ./config.json
```

Use `config.example.json` as a starting point and write local configuration to `config.json`. Local config, token data, logs, and `data/` are ignored by Git.

## Clean-Room Note

TDropFarmer is intended as an independent implementation. Do not copy code, GraphQL operation collections, internal structure, comments, or naming from GPL-licensed Twitch miners. Use official Twitch documentation, self-authored protocol notes, and independently observed behavior.

## Disclaimer

This project interacts with Twitch behavior that may be undocumented or unstable. Twitch may change APIs or protocols without notice, and automation can carry account risk. Use at your own risk.
