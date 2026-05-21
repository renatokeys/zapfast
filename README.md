# zapfast

[![CI](https://github.com/renatokeys/zapfast/actions/workflows/ci.yml/badge.svg)](https://github.com/renatokeys/zapfast/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/renatokeys/zapfast.svg)](https://pkg.go.dev/github.com/renatokeys/zapfast)

`zapfast` is a self-hostable WhatsApp HTTP API. It is a hardened fork of
[`asternic/wuzapi`](https://github.com/asternic/wuzapi) (MIT) that wraps the
[`whatsmeow`](https://github.com/tulir/whatsmeow) WebSocket library and is
being progressively refactored into a hexagonal architecture with a
drop-in [Z-API](https://z-api.io)-compatible request/response shape.

> **Status:** Phase F0 — fork baseline. Hexagonal migration starts in F1.
> See [`docs/spec/07-fork-plan.md`](https://github.com/Hivenzz/whatsmeow-api/blob/main/docs/spec/07-fork-plan.md) for the full roadmap.

## Why

- **Scale**: per-instance supervisors, Postgres outbox for webhook delivery,
  graceful K8s drain, Prometheus + OpenTelemetry.
- **Compatible**: drop-in path-shape compatibility with Z-API so existing
  integrations swap one base URL.
- **Hardened**: TLS verification on by default, no bundled dashboard, no
  stdio/MCP transport, no SQLite, no in-process RabbitMQ.
- **Cloud native**: distroless image, Helm chart, GitOps-friendly manifests.

## Attribution

`zapfast` is derived from **wuzapi** by [asternic](https://github.com/asternic),
released under the MIT license. The original `LICENSE` and copyright are
preserved verbatim. See [`NOTICE.md`](NOTICE.md) for the full attribution and
the list of substantial modifications applied in this fork.

## Quick start

```bash
git clone https://github.com/renatokeys/zapfast.git
cd zapfast
cp .env.sample .env   # edit DB_*, tokens, webhooks
docker compose up -d
```

By default the API listens on `:8080`. Health probe: `GET /health`.

### Environment

| Variable                     | Description                                     |
| ---------------------------- | ----------------------------------------------- |
| `DB_HOST` / `DB_PORT`        | Postgres host and port.                         |
| `DB_USER` / `DB_PASSWORD`    | Postgres credentials.                           |
| `DB_NAME` / `DB_SSLMODE`     | Database name and SSL mode (`disable`/`require`).|
| `WUZAPI_ADMIN_TOKEN`         | Admin token for `/admin/*` endpoints.           |
| `WUZAPI_GLOBAL_ENCRYPTION_KEY` | 32-byte key for at-rest encryption.           |
| `WUZAPI_GLOBAL_HMAC_KEY`     | Global HMAC key for webhook signing.            |
| `WUZAPI_GLOBAL_WEBHOOK`      | Optional global webhook URL (all instances).    |
| `WEBHOOK_RETRY_*`            | Retry policy for webhook delivery.              |

### Build from source

```bash
make build            # ./bin/zapfast and ./bin/zapfast-worker
make test             # race-enabled tests
make lint             # golangci-lint, strict config
make docker-build     # distroless image
```

## Architecture

```
Client → HTTP (cmd/api) → app/use-cases → ports → adapters
                                                    ├── whatsmeow (WhatsApp)
                                                    ├── postgres   (state + outbox)
                                                    ├── webhook    (HTTP delivery)
                                                    └── storage    (S3-compatible)

cmd/worker (F2) → Postgres outbox consumer → webhook delivery + DLQ
```

Layout (target, scaffold is in place; legacy code lives under `cmd/api/`
until migrated):

```
cmd/{api,worker}/
internal/{domain,ports,app,adapters,config,observability}/
pkg/
api/openapi/
deployments/{helm,k8s}/
test/{integration,e2e,fixtures,golden}/
docs/
```

## Roadmap

| Phase | Focus                                                                | Status |
| ----- | -------------------------------------------------------------------- | ------ |
| F0    | Fork baseline (this PR-0): cleanup, scaffold, CI                     | done   |
| F1    | Hexagonal skeleton + first feature (SendText) end-to-end             | next   |
| F2    | Migrate all messaging + receive; Postgres outbox + worker            | -      |
| F3    | Z-API auth shape, PIX / ReviewPay buttons, Redis idempotency         | -      |
| F4    | Calls adapter (port + stub for `wa-calls-go`)                        | -      |
| F5    | Video / Vision adapter ports                                         | -      |

## Contributing

PRs welcome — please read `docs/spec/` first to understand the architectural
direction. Conventional Commits enforced, strict golangci-lint, tests
required for new code.

## License

[MIT](LICENSE) — same as the upstream wuzapi project. See [`NOTICE.md`](NOTICE.md)
for attribution details.
