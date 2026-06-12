# Research notes: local-first Teller finance CLI

Date: 2026-06-12
Workspace: `/home/exedev/development/lasso-money`

## User framing

The target is **not** a venture SaaS API product and not a hosted bank-data aggregator. It is a local CLI/MCP that someone can run on their own machine with their own Teller keys/certs. Plaid is explicitly out of scope for the MVP.

## Existing local evidence

Checked local setup:

```text
teller.env exists
teller enrollment exists
plaid .env missing
```

So the live working substrate on this machine is already Teller, not Plaid.

Existing codebase inspected earlier:

- `/home/exedev/.hermes/sources/plaid-mcp/src/plaid_mcp/teller_cli.py`
  - has `plaid-mcp teller connect`
  - has `plaid-mcp teller probe`
  - has `plaid-mcp teller whoami`
  - stores enrollment at `~/.plaid-mcp/teller/enrollment.json` with restrictive permissions
- `/home/exedev/.hermes/sources/plaid-mcp/src/plaid_mcp/providers/teller.py`
  - uses `https://api.teller.io`
  - uses access token as HTTP Basic Auth username
  - requires mTLS cert/key in `development` and `production`
  - supports accounts, balances, transactions, identity
- Wrapper exists:
  - `/home/exedev/.local/bin/plaid-mcp-teller-hermes`
  - sources `/home/exedev/.plaid-mcp/teller/teller.env`

## Teller docs findings

### Auth model

Source: https://teller.io/docs/api/authentication

Teller uses two layers:

1. **mTLS client certificate**
   - Required for all requests involving end-user data in `development` and `production`.
   - Not required in `sandbox`, because sandbox does not involve real end-user data.
   - Private key must remain secret and never be embedded in distributed/mobile apps.

2. **Access token from Teller Connect**
   - Created when a user completes Teller Connect.
   - Represents the user’s consent for a financial institution enrollment.
   - Used as HTTP Basic Auth username:

```bash
curl -u ACCESS_TOKEN: https://api.teller.io/accounts
```

Important design implication: a local CLI can store the application cert/key path in config and store the enrollment token locally with chmod `600`. It should never print full token values.

### Teller Connect

Source: https://teller.io/docs/guides/connect

Teller Connect is a client-side UI component that handles:

- institution selection
- credential validation
- MFA
- account selection
- error handling

On success it returns an enrollment object containing:

- `accessToken`
- `user.id`
- `enrollment.id`
- `enrollment.institution.name`
- optional signatures

Existing local implementation already follows this shape by launching a localhost page with `https://cdn.teller.io/connect/connect.js` and capturing `onSuccess`.

### Accounts

Source: https://teller.io/docs/api/accounts

`GET /accounts` returns accounts granted during enrollment. Account fields include:

- `id`
- `enrollment_id`
- `institution.id/name`
- `name`
- `type`: `depository` or `credit`
- `subtype`: e.g. `checking`, `savings`, `credit_card`
- `currency`
- `last_four`
- `status`: `open` or `closed`
- `links`: self/details/balances/transactions depending on institution support

Design implication: the CLI should inspect `links` and degrade gracefully when an account does not support balances or transactions.

### Balances

Source: https://teller.io/docs/api/account/balances

`GET /accounts/{id}/balances` returns live, real-time balances.

Fields:

- `account_id`
- `ledger`
- `available`
- at least one of ledger/available is always present

Design implication: for credit cards, expose both raw names and friendly labels. Do not overclaim `credit limit` or `minimum payment`; Teller balance docs do not expose those.

### Transactions

Source: https://teller.io/docs/api/account/transactions

`GET /accounts/{id}/transactions` exposes ledger transactions.

Fields include:

- `id`
- `account_id`
- `amount` as signed string
- `date`
- `description`
- `status`: `posted` or `pending`
- `running_balance`, only for posted where available
- `type`, e.g. `card_payment`
- `details.processing_status`: `pending` or `complete`
- `details.category`: Teller category
- `details.counterparty.name/type`

Filtering/pagination:

- `count`
- `from_id`
- `start_date`
- `end_date`

Docs note initial calls can time out for accounts with many transactions; retry after a few seconds.

Design implication: MVP needs robust retry/backoff and date-window sync. A persistent local cache should upsert by transaction ID.

### Institutions

Source: https://teller.io/docs/api/institutions

`GET /institutions` is public, beta, unpaginated, no auth required. Returns:

- `id`
- `name`
- `products`

Live check returned `7008` institutions. Search for “American Express” in the public list returned no direct obvious Amex institution name, but our local Teller enrollment exists and user says the Amex setup works. Therefore product should not rely solely on institution-list search for support detection; it should support whatever current enrollment works.

### Errors

Source: https://teller.io/docs/api/errors

Status codes:

- `400`: missing certificate, bad request
- `401`: missing access token
- `403`: invalid/revoked access token
- `404`: not found, including disconnected enrollment errors
- `410`: permanent gone, e.g. closed account
- `422`: invalid body
- `429`: rate limited
- `502`: financial institution unavailable

Enrollment disconnected errors include:

- `enrollment.disconnected`
- `enrollment.disconnected.account_locked`
- `enrollment.disconnected.credentials_invalid`
- `enrollment.disconnected.enrollment_inactive`
- `enrollment.disconnected.user_action.captcha_required`
- `enrollment.disconnected.user_action.contact_information_required`
- `enrollment.disconnected.user_action.insufficient_permissions`
- `enrollment.disconnected.user_action.mfa_required`
- `enrollment.disconnected.user_action.web_login_required`

Design implication: `money doctor` and transaction fetch failures should translate these into human next steps, e.g. “run `money reconnect`”.

### Webhooks

Source: https://teller.io/docs/api/webhooks

Useful event types:

- `transactions.processed`
- `enrollment.disconnected`
- `account.number_verification.processed`
- `webhook.test`

Teller polls institutions multiple times per day and guarantees at least one polling attempt every 24 hours.

Design implication: webhooks are post-MVP for local CLI because they require a reachable URL/tunnel. MVP should use manual/cron sync. Later: optional `money webhook listen` with local tunnel instructions.

## Market landscape / alternatives

The requested “last30days” skill was not currently installed in this Hermes profile despite memory mentioning it; I used current web/GitHub search instead and found recent 2026 projects.

### 1. `zrabin/personal-finance-mcp`

Source: https://github.com/zrabin/personal-finance-mcp

Recent 2026 Python MCP server.

Positioning:

- Chase bank accounts and credit cards via Teller API
- Venmo CSV import
- local SQLite storage
- rich query tools: spending summaries, cash flow, monthly trends, transaction search

Useful lessons:

- Local SQLite is a common expected pattern.
- MCP-only is useful, but CLI remains under-served.
- Tools map well to our MVP: `sync`, `get_accounts`, `get_balances`, `get_transactions`, `get_spending_summary`, `get_cash_flow`, `get_monthly_trend`, `enroll_account`.

Gap we can exploit:

- Create a polished CLI first, with MCP as a companion mode, instead of only MCP tools.

### 2. `elcukro/bank-mcp`

Source: https://github.com/elcukro/bank-mcp

Recent TypeScript MCP server supporting Teller, Plaid, Enable Banking, Tink, mock provider.

Notable claims:

- read-only by design
- local stdio transport
- no cloud relay
- no telemetry
- in-memory cache per process
- setup wizard via `npx @bank-mcp/server init`

Teller setup notes from README:

- start with sandbox; no certs needed
- development/production prompt for mTLS certificate paths
- free tier supports up to 100 live connections

Useful lessons:

- Setup wizard is important.
- Mock mode matters for demos/tests.
- Provider abstraction can be thin, but for our MVP we should stay Teller-only.

Gap:

- It is MCP-first, TypeScript, multi-provider. Our wedge can be Python/local CLI + Teller-only, optimized for personal automation and scripts.

### 3. `sebinsua/teller-cli`

Source: https://github.com/sebinsua/teller-cli

Deprecated Rust CLI, latest release 2017.

Interesting UX ideas:

- `teller show balance current`
- `teller list transactions current | grep "NANNA'S"`
- `teller list balances business --interval=monthly --timeframe=year --output=spark | spark`
- low-balance alert shell scripts
- menu bar spending tracker scripts

Useful lesson:

- A CLI should not merely mirror API endpoints. It should provide shell-native finance workflows: thresholds, month-to-date spend, merchant search, sparkline/chart output, CSV export.

Gap:

- Old/deprecated and UK-era Teller assumptions. A modern US Teller CLI seems open.

### 4. Teller examples

Source: https://github.com/tellerhq/examples

Official-ish examples across languages. Useful for validating Connect flow and environment variables:

- `APP_ID`
- `ENV`
- `CERT`
- `CERT_KEY`

Sandbox credentials:

- username: `username`
- password: `password`

Useful lesson:

- MVP can include a sandbox-mode happy path and tests against mocked HTTP or sandbox examples.

### 5. SimpleFIN / OpenCoffer / Actual Budget ecosystem

Sources:

- https://bridge.simplefin.org/
- https://github.com/osirishorus/opencoffer
- https://actualbudget.org/docs/advanced/bank-sync/simplefin

SimpleFIN costs:

- $1.50/month or $15/year
- up to 25 institutions and 25 apps

Actual Budget docs note SimpleFIN generally updates about once/day and may sync at most 90 days depending on setup.

Useful lesson:

- SimpleFIN is a strong “BYO local personal finance” alternative, but less live/API-like than Teller.
- OpenCoffer shows the full app/dashboard route; we should avoid building a big app in MVP.

### 6. Actual/Teller sync projects

Search found `noelpena/teller-actual-sync`, a Dockerized Teller-to-Actual Budget sync tool.

Useful lesson:

- Export/sync into existing finance tools is valuable.
- MVP should include CSV/JSON export; Actual Budget direct sync can be later.

## Market conclusion

There are several MCP servers and full apps, but the specific wedge remains compelling:

> A local-first, Teller-only CLI that is pleasant for scripts and agents, stores a SQLite cache locally, and exposes an optional MCP server over the same query layer.

The closest projects are MCP-first, multi-provider, or full-app/dashboard. A clean CLI with `money tx/search/spend/export/doctor/connect` is still differentiated.
