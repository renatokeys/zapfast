## NOTICE

zapfast is a fork of [wuzapi](https://github.com/asternic/wuzapi) by asternic, MIT-licensed.
This project preserves the original copyright and adds substantial modifications.

Modifications copyright (C) 2026 Hivenzz / Renato Oliveira.

The original wuzapi MIT LICENSE is preserved verbatim in the `LICENSE` file.

### Substantial modifications include

- Removal of stdio (MCP/LLM) transport mode.
- Removal of bundled static dashboard (admin moves to API + Grafana).
- Removal of SQLite support (Postgres-only).
- Removal of in-process RabbitMQ webhook fallback (replaced by Postgres outbox).
- Hardened webhook TLS validation (`InsecureSkipVerify` removed).
- Hexagonal architecture scaffold and incremental migration.
- Drop-in Z-API request/response shape compatibility (in progress).
- New domain features (PIX button, ReviewPay, list sections, etc).
- Multi-stage distroless Dockerfile, Kubernetes / Helm deployment artifacts.
- Strict `golangci-lint` configuration, expanded test coverage tooling.

For the historical wuzapi changelog, see the upstream repository.
