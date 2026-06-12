# MVP scope: Teller-powered local finance CLI

Date: 2026-06-12

## Product thesis

Build a **local-first Teller CLI** for personal finance automation:

- user brings their own Teller application ID + cert/key
- account enrollment happens locally via Teller Connect
- access tokens and synced data stay on disk locally
- commands are useful in a shell without an LLM
- the same local data/query layer can expose MCP tools for Hermes/Claude/Cursor

Forget Plaid for MVP. Keep the design simple and Teller-native.

## Name

Candidate names:

1. `money` — best if we want generic personal-finance CLI over time.
2. `lasso` — clear, avoids colliding with existing commands/packages.
3. `amex` — useful for our immediate use, but too narrow if Teller enrolls multiple banks/cards.

Recommendation for MVP repo/package:

```text
lasso
```

Expose optional aliases later:

```bash
amex='lasso --profile amex'
money='lasso'
```

## Non-goals

MVP should **not** include:

- Plaid
- payments / transfers / Zelle / money movement
- account number/routing detail retrieval
- card controls
- bill pay
- reward points
- statement PDF retrieval
- hosted SaaS
- multi-user accounts
- web dashboard
- remote HTTP server by default

Everything should be read-only against bank data except local cache/config writes.

## CLI command surface

### Setup / health

```bash
lasso init
lasso doctor
lasso connect
lasso whoami
lasso reconnect
lasso unlink
```

Details:

- `init`: create config directory, prompt for Teller env/app id/cert/key paths, write `.env` or config TOML with secret-safe permissions.
- `doctor`: verify app id, environment, cert/key path presence, cert/key permissions, enrollment file, API connectivity, local DB migrations.
- `connect`: launch localhost Teller Connect flow and store enrollment token.
- `whoami`: print enrollment metadata with token redacted.
- `reconnect`: Teller Connect update-mode if supported; otherwise run connect and replace enrollment.
- `unlink`: revoke local enrollment / optionally `DELETE /accounts` upstream with explicit confirmation.

### Accounts and balances

```bash
lasso accounts
lasso accounts --json
lasso account <account-selector>
lasso balances
lasso balance <account-selector>
```

Account selector should accept:

- full account id
- last four
- substring of account name
- alias stored locally

### Transactions

```bash
lasso tx --since 30d
lasso tx --from 2026-06-01 --to 2026-06-12
lasso tx --account gold --limit 50
lasso tx get <transaction-id>
lasso search "amazon" --since 90d
lasso export tx --since month --format csv --out tx.csv
```

Filters:

- `--account`
- `--from YYYY-MM-DD`
- `--to YYYY-MM-DD`
- `--since 7d|30d|90d|month|last-month|year|ytd`
- `--pending/--posted/--all`
- `--min AMOUNT`
- `--max AMOUNT`
- `--category`
- `--merchant` / `--counterparty`
- `--limit`

Formats:

- `pretty`
- `json`
- `jsonl`
- `csv`
- eventually `md`

### Sync/cache

```bash
lasso sync --since 90d
lasso sync --account gold --since 30d
lasso cache status
lasso cache vacuum
lasso cache clear
```

Important: Teller has live date-range APIs, but a local CLI should still maintain a cache for speed, search, summaries, and offline-ish workflows.

Sync algorithm:

1. Resolve accounts.
2. For each account, fetch date window using `start_date` and `end_date`.
3. If response size reaches page limit, continue with `from_id` pagination.
4. Upsert transactions by Teller transaction ID.
5. Include a 7–10 day overlap on incremental sync to catch pending→posted/date shifts, matching Teller doc guidance.
6. Store sync run metadata and errors.

### Analysis

```bash
lasso spend --since month
lasso spend --group merchant --since 90d
lasso spend --group category --since month
lasso cashflow --since 6mo
lasso merchants top --since 90d
lasso recurring --since 12mo
lasso anomalies --since 90d
```

MVP analysis commands:

1. `spend --group merchant|category|account|month`
2. `merchants top`
3. `cashflow`

Post-MVP:

- recurring/subscription detection
- anomaly detection
- category rule management
- budget thresholds
- low-balance alerts

### MCP mode

```bash
lasso mcp serve
lasso mcp config --client hermes
lasso mcp config --client claude-desktop
```

MCP tools should mirror deterministic CLI query functions:

- `list_accounts`
- `get_balances`
- `list_transactions`
- `search_transactions`
- `spending_summary`
- `cash_flow`
- `top_merchants`
- `sync`
- `doctor/status`

Tool annotations should mark everything read-only except `sync` / local config writes; even `sync` mutates only local cache.

## Data model

Use SQLite at:

```text
~/.lasso/lasso.db
```

Config/secrets:

```text
~/.lasso/config.toml       # app id, env, cert/key paths; chmod 600
~/.lasso/enrollments.json  # access tokens; chmod 600
~/.lasso/logs/             # optional logs; no secrets
```

Tables:

### enrollments

- `id` Teller enrollment ID
- `institution_id`
- `institution_name`
- `access_token_encrypted_or_file_ref` initially maybe plaintext file chmod 600; later OS keychain/encryption
- `created_at`
- `updated_at`
- `status`

### accounts

- `id`
- `enrollment_id`
- `institution_id`
- `institution_name`
- `name`
- `alias`
- `type`
- `subtype`
- `last_four`
- `currency`
- `status`
- `raw_json`
- timestamps

### balances

- `account_id`
- `ledger`
- `available`
- `as_of`
- `raw_json`

### transactions

- `id`
- `account_id`
- `amount`
- `currency`
- `date`
- `description`
- `counterparty_name`
- `counterparty_type`
- `category`
- `status`
- `type`
- `running_balance`
- `raw_json`
- timestamps

Indexes:

- `(account_id, date)`
- `date`
- `lower(description)` maybe FTS later
- `counterparty_name`
- `category`

### sync_runs

- `id`
- `started_at`
- `finished_at`
- `account_id`
- `start_date`
- `end_date`
- `status`
- `fetched_count`
- `upserted_count`
- `error_code`
- `error_message`

## Architecture

```text
cmd/lasso/main.go       # entrypoint
internal/cli/                  # command routing, help, flags
internal/config/               # config loading, paths, permission checks
internal/teller/               # low-level Teller HTTP + mTLS client
internal/connect/              # localhost Teller Connect flow
internal/store/                # SQLite schema/queries
internal/selectors/            # account aliases/id/last4 resolution
internal/formatters/           # pretty/json/jsonl/csv output
internal/sync/                 # sync/upsert logic
internal/analysis/             # spend/cashflow/top merchant calculations
internal/mcp/                  # MCP server over same service layer
internal/apperrors/            # Teller errors → user-friendly next steps
```

Recommendation: Go for the product CLI, with the existing Python `plaid-mcp` Teller implementation used only as a reference/donor for API semantics.

Core Go stack:

- stdlib `net/http` with `crypto/tls` for Teller mTLS
- stdlib `flag` initially; consider Cobra only if command complexity demands it
- `database/sql` with SQLite driver for local cache
- stdlib `encoding/json` and `encoding/csv` for scriptable outputs
- small internal packages, no framework-heavy structure early

Why Go now:

1. **Mercury-style distribution** — easier eventual single-binary install.
2. **Fast startup** — feels right as a shell-native finance tool.
3. **Native mTLS** — Teller’s cert/key model maps cleanly to Go.
4. **Lower runtime friction** — no Python/venv/pipx/uv expectation for end users.
5. **Still can borrow from Python** — the existing Teller MCP code validates command semantics and edge cases without dictating implementation language.

Do not continue mutating `plaid-mcp`; this should be a fresh Teller-only Go codebase.

## UX principles

1. **Local-first by default**
   - No cloud service.
   - No telemetry.
   - No remote HTTP listener by default.

2. **Scriptable outputs**
   - Every data command supports JSON/CSV.
   - Pretty output should be human-readable but not the only interface.

3. **Safe secrets**
   - Never print full access token.
   - Config and enrollment files chmod `600`.
   - `doctor` reports presence/validity, not secret values.

4. **Read-only finance**
   - No money movement.
   - No account-number/routing retrieval in MVP.
   - Upstream `DELETE /accounts` only behind explicit `unlink --revoke-upstream --yes` style confirmation.

5. **Graceful institution differences**
   - Inspect account `links`.
   - If balances or transactions are unavailable for an account, show that cleanly.

6. **Errors explain next action**
   - Missing cert: configure cert/key path.
   - 403: token revoked; reconnect.
   - disconnected MFA/captcha/web-login: reconnect or login to institution.
   - 429/502: retry later/backoff.

## MVP milestone plan

### Milestone 0 — scaffold

Deliverables:

- Python package skeleton under this folder
- `uv` project
- `lasso --help`
- test harness
- config paths and permission checks

Acceptance:

```bash
uv run lasso --help
uv run pytest
```

### Milestone 1 — connect + doctor

Deliverables:

- `init`
- `doctor`
- `connect`
- `whoami`

Acceptance:

- sandbox Connect works with Teller test credentials
- enrollment file created chmod `600`
- token redacted in `whoami`
- `doctor` detects missing certs in development/production

### Milestone 2 — accounts/balances

Deliverables:

- Teller client
- SQLite account/balance cache
- `accounts`
- `balances`
- account selector resolution

Acceptance:

- works against existing enrollment
- JSON and pretty output
- no secret leakage in logs/output

### Milestone 3 — transactions + export

Deliverables:

- `sync`
- `tx`
- `search`
- `export tx --format csv|json|jsonl`
- retry/backoff for initial transaction timeout

Acceptance:

- can sync 30/90 days
- upserts by transaction ID
- filters by account/date/query
- CSV opens in spreadsheet/budget tools

### Milestone 4 — analysis

Deliverables:

- `spend --group merchant|category|month|account`
- `cashflow`
- `merchants top`

Acceptance:

- outputs match SQL aggregates
- handles pending vs posted filters
- negative/positive sign convention documented clearly

### Milestone 5 — MCP

Deliverables:

- `mcp serve`
- deterministic read tools over same query layer
- config snippets for Hermes/Claude Desktop/Cursor

Acceptance:

- local MCP can list accounts, search transactions, and summarize spend
- `sync` tool clearly labeled as local-cache mutation only

## Open questions

1. Package name: `lasso`, `money`, or `bank-cli`?
2. Should we start by extracting from `plaid-mcp`, or fresh package with copied/adapted Teller code?
3. Should enrollment support multiple institutions/profiles in MVP, or just one active enrollment?
4. Should local tokens be plaintext chmod `600` initially, or encrypted using keyring/OS secret store?
5. Should default command semantics fetch live data, cache data, or use cache unless `--live`?

Recommended answers:

1. Package: `lasso`; optional `money` alias.
2. Fresh package, copy/adapt only Teller pieces from `plaid-mcp`.
3. Support multiple enrollments in schema from day one, but MVP UX can assume one default.
4. Plaintext chmod `600` for MVP, with clear warning; keyring later.
5. `balances` live by default; `tx/search/spend` use cache and recommend/auto-run `sync` when stale.

## MVP command examples

```bash
# one-time setup
lasso init

# connect an account/card
lasso connect

# verify local state
lasso doctor

# account data
lasso accounts
lasso balances

# transaction cache
lasso sync --since 90d

# queries
lasso tx --since 30d
lasso search "delta" --since 1y
lasso spend --group merchant --since month
lasso merchants top --since 90d

# export
lasso export tx --since last-month --format csv --out last-month.csv

# agent mode
lasso mcp serve
```

## Recommendation

Proceed with a fresh `lasso` package in this folder, backed by Teller only. Build CLI first, MCP second, and treat local SQLite as the product’s core asset.
