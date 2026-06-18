# Ponytail Simplification Proposal

## Goal

Reduce duplicate interfaces, dead persistence, and committed tooling artifacts without changing Lasso's core behavior: connect to Teller, cache transactions, query finances, and export results.

## Proposed cuts

### 1. Remove generated Playwright artifacts

- Delete tracked `.playwright-cli/` logs and snapshots.
- Add `.playwright-cli/` to `.gitignore`.

**Estimated reduction:** 954 lines, 16 files.

### 2. Keep one CLI grammar

Use the short commands already featured throughout `README.md`:

- `accounts`
- `balances`
- `sync`
- `tx`
- `search`
- `spend`
- `merchants top`
- `cashflow`
- `export tx`
- `cache status`

Remove the parallel resource grammar (`account list`, `balance list`, `transaction list`, and similar), including routing wrappers, normalization branches, alias documentation, and focused tests.

**Estimated reduction:** 100–120 lines.

### 3. Keep one JSON interface

Retain the stable `--format json` envelope and remove legacy per-command `--json` flags and duplicate encoder branches.

Update help and documentation to direct callers to `--format json`.

**Estimated reduction:** 45–55 lines.

### 4. Complete the balance cache

Lasso is local-first and `sync` already writes balances, so add the missing read path rather than delete the cache.

- Add `Store.CachedBalances()` with account metadata and `as_of`.
- Give cached balances a fixed five-minute TTL.
- Make `balances` return cached rows when all selected balances are fresh; otherwise fetch fresh balances from Teller and update the cache.
- Add `balances --live` to bypass the TTL, fetch from Teller, and update the cache.
- Keep `sync` refreshing balances regardless of TTL and keep cache status reporting their count.
- Clearly display `as_of` in text and JSON output so freshness is visible.
- Add one focused store/CLI check covering fresh cache, expired cache, and `--live`.

Teller documents `GET /accounts/{account_id}/balances` at 10 requests per minute per account. Five minutes is intentionally conservative for this user-invoked CLI and requires no scheduler, background worker, or configurable TTL. Source: [Teller account balance API](https://docs.teller.io/api/account-balances).

**Estimated addition:** 30–50 lines.

### 5. Remove unused persisted data

Stop storing values that have no read path:

- `raw_json` columns and marshaling
- `accounts.alias`
- unused `created_at` and `updated_at` columns
- `sync_runs.error_message` unless sync diagnostics will expose it

Because existing databases may contain the old columns, leave them in place rather than adding a destructive migration; new writes and fresh schemas simply stop depending on them.

**Estimated reduction:** 18–25 lines, plus smaller new databases.

### 6. Remove dead and no-op API surface

Remove:

- unused `store.SyncRun`
- unused package-level `cli.Run`
- unpopulated `envelope.ConnectionID`
- unpopulated `nextAction.Params`
- speculative `--connection` documentation
- no-op `--all` transaction flag

**Estimated reduction:** 25–30 lines.

### 7. Use Go 1.26 standard library helpers

- Replace manual random hex helpers with `crypto/rand.Text` where compatible with the required output contract.
- Replace the local `fallback` helper with `cmp.Or`.

**Estimated reduction:** 15–20 lines.

## Intentionally retained

- `modernc.org/sqlite`: the only direct dependency and the required `database/sql` SQLite driver.
- Teller client retry and pagination logic: real network behavior, not speculative abstraction.
- Local environment-file parsing: small, dependency-free, and tailored to the documented config format.
- Structured JSON envelopes and error types: part of the agent-facing contract.
- Separate `internal` packages: they represent distinct Teller, storage, configuration, connection, and CLI responsibilities rather than one-implementation indirection.

## Implementation order

1. Remove `.playwright-cli/` artifacts and update `.gitignore`.
2. Remove dead fields, types, helpers, and `--all`.
3. Complete the five-minute balance-cache read path and remove only genuinely unused schema fields.
4. Remove legacy `--json` output.
5. Choose and enforce the single CLI grammar; update `README.md`, `SETUP.md`, help, schemas, and tests together.
6. Run formatting and validation:

```sh
gofmt -w cmd internal
go test ./...
go vet ./...
```

## Compatibility impact

The CLI grammar and `--json` removals are breaking changes. Make them before a stable `1.0` release, or split them into a later major release if users already depend on those forms. The remaining cuts should not affect documented core workflows.

## Expected result

Approximately **1,130–1,170 fewer tracked lines**, **16 fewer generated files**, and **no dependency changes**, while giving cached balances a five-minute freshness policy.
