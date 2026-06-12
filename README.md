# Lasso Money

Local-first finance data CLI/MCP for agents. The current MVP is Teller-powered, with a provider-agnostic `lasso` command surface.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/DannyAziz/lasso-money/main/install.sh | sh
```

Or with a Go toolchain:

```bash
go install github.com/dannyaziz/lasso-money/cmd/lasso@latest
```

Then run setup:

- **Humans**: `lasso init`, then `lasso doctor` and follow its output.
- **Agents**: read [SETUP.md](SETUP.md) — `lasso doctor --format json` is the
  setup state machine, and `lasso connect --no-open --format json` emits the
  Connect URL as a JSON event for relaying to a human.

## Decision

Build a **Teller-only Go CLI** first under the **Lasso Money** OSS product name. Forget Plaid for the MVP.

Why Go now:
- Best fit for a Mercury-style installable CLI.
- Single-binary distribution later.
- Native mTLS/HTTP support.
- Fast startup and clean shell ergonomics.
- Python remains useful as reference/donor code from the existing Teller MCP, but the product surface should be Go.

## Current implementation

```bash
go test ./...
go build -o ./bin/lasso ./cmd/lasso
./bin/lasso --help
```

Implemented:
- Go module: `github.com/dannyaziz/lasso-money`
- Entry point: `cmd/lasso/main.go`
- Root CLI/help/version: `internal/cli`
- Agent-first affordances inspired by Stripe Link CLI:
  - `--llms` and `--llms-full` JSON command guides
  - `schema` / `schema <command>` command metadata
  - global `--format json` envelope output for core commands
  - canonical resource aliases such as `account list`, `transaction list`, `merchant top`
- `init` config template command
- `doctor` config/enrollment verifier that does not print secrets; `--format json` emits per-check statuses with fix hints (the agent setup state machine)
- `connect` localhost Teller Connect flow; `--format json` emits a `connect.url` event line for agent relay
- `whoami` with redacted access token
- `accounts` live Teller accounts command
- `balances` live Teller balances command
- `tx` cached transaction window command (`--live` fetches directly from Teller)
- `sync` live Teller → local SQLite cache
- `search` cached transaction search
- `spend` cached spending summaries by merchant/category/account/month
- `merchants top` cached merchant leaderboard
- `cashflow` cached monthly inflow/outflow/net
- cached transaction filters: status, min/max amount, category, merchant/counterparty
- `export tx` cached CSV/JSON/JSONL export
- `cache status` cache inspection
- structured error envelopes (`ok: false`) on stdout with `--format json`
- semantic exit codes (see `lasso --llms-full`)
- retry with backoff for retryable Teller responses (429/502/504, network errors)
- Config env parser: `internal/config`
- Teller API client/enrollment handling: `internal/teller`
- Tests for config parsing, enrollment storage, Teller client request behavior, store queries, and CLI parsing/envelopes

## Local config

Default files:

```text
~/.lasso/config.env
~/.lasso/enrollment.json
```

Config keys:

```text
TELLER_APPLICATION_ID=
TELLER_ENV=sandbox
TELLER_CERT_PATH=
TELLER_KEY_PATH=
TELLER_ENROLLMENT_PATH=
TELLER_DB_PATH=
```

## MVP target

```bash
lasso init
lasso doctor
lasso connect
lasso whoami
lasso accounts
lasso balances
lasso tx --account gold --since 30d
lasso sync --since 90d
lasso search "amazon" --since 90d
lasso spend --group merchant --since month
lasso export tx --since month --format csv --out transactions.csv
```

## Agent-first command surface

Use the Stripe-style discovery commands before invoking the CLI from an agent:

```bash
lasso --llms
lasso --llms-full
lasso schema
lasso schema transaction.list
```

Prefer canonical resource commands and `--format json` envelopes:

```bash
lasso account list --format json
lasso balance list --format json
lasso sync run --format json
lasso transaction list --since ytd --merchant amazon --format json
lasso transaction search amazon --since ytd --format json
lasso spend summary --group merchant --since month --format json
lasso merchant top --since 90d --format json
lasso cashflow summary --since 6mo --format json
lasso cache status --format json
```

Envelope shape:

```json
{
  "ok": true,
  "schema_version": "2026-06-12",
  "command": "transaction.list",
  "data": [],
  "meta": { "count": 0, "source": "cache", "truncated": false },
  "warnings": [],
  "next_actions": []
}
```

Errors with `--format json` emit an `ok: false` envelope on stdout and exit
with a semantic code (0 success, 2 usage, 3 not found, 4 auth/config,
5 conflict, 6 upstream unavailable, 7 retryable network error):

```json
{
  "ok": false,
  "schema_version": "2026-06-12",
  "command": "transaction.list",
  "error": {
    "code": "config_error",
    "message": "load config ~/.lasso/config.env: no such file or directory",
    "retryable": false,
    "fix": "run `lasso init` to create a config"
  },
  "warnings": [],
  "next_actions": []
}
```

## Sign conventions

Teller signs amounts from the account's perspective: credit-card charges
arrive positive, while depository (checking/savings) debits arrive negative.
`spend`, `merchant top`, and `cashflow` normalize this per account type so
that **positive always means money out** — credit amounts are used as-is and
depository amounts are negated. Accounts with an unknown type are treated as
credit. Raw `transaction list` output keeps Teller's original signs.

Legacy aliases still work:

```text
accounts -> account list
balances -> balance list
tx -> transaction list
search -> transaction search
export tx -> transaction export
spend -> spend summary
merchants top -> merchant top
cashflow -> cashflow summary
sync -> sync run
```

## Files

- `SETUP.md` — install + setup playbook for humans and agents.
- `docs/research-notes.md` — source-grounded research on Teller docs and market landscape.
- `docs/mvp-scope.md` — recommended product scope, command surface, architecture, milestones.
- `docs/agent-first-stripe-alignment.md` — Stripe Link CLI-inspired agent-first plan and UX contract.
