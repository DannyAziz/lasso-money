# Lasso Money setup

This is the setup playbook for **lasso**, a local-first read-only Teller
finance CLI. It is written so an AI agent can drive the whole setup on a
human's behalf — over chat, SSH, or locally — but it works for humans too.

**Human in a hurry:**

```bash
curl -fsSL https://raw.githubusercontent.com/DannyAziz/lasso-money/main/install.sh | sh
lasso init      # then edit ~/.lasso/config.env
lasso doctor    # follow its output until it prints "ready"
lasso connect   # link a bank in the browser
lasso sync      # cache transactions locally
```

The rest of this document is the agent protocol.

## How setup works

`lasso doctor --format json` is the setup state machine. It returns a list
of checks, each with `status` (`ok` | `missing` | `warn` | `skipped`) and a
`fix` hint, plus `meta.ready` and `next_actions`. The protocol is:

> Fix the first `missing` check, re-run doctor, repeat until `meta.ready`
> is `true`. Then connect and sync.

Every command supports `--format json` and returns a stable envelope
(`ok`, `command`, `data`, `meta`, `warnings`, `next_actions`). Failures
return `ok: false` with `error.code`, `error.retryable`, and `error.fix`,
plus a semantic exit code. Discover the full command surface with
`lasso --llms` and `lasso schema`.

## What you will need from the human

Ask for these up front (or when the corresponding doctor check fails):

1. **Teller application ID** (`app_...`) — from https://dashboard.teller.io
   after creating a free Teller account.
2. **Environment** — `sandbox` (fake data, no certs, good for trying it
   out), or `development` / `production` (real bank data; requires the
   mTLS certificate + key downloaded from the Teller dashboard).
3. If not sandbox: **paths to the mTLS cert and key files** on this
   machine. Never read or print the contents of these files.

## Step 0 — install

```bash
curl -fsSL https://raw.githubusercontent.com/DannyAziz/lasso-money/main/install.sh | sh
```

Alternative with a Go toolchain: `go install github.com/dannyaziz/lasso-money/cmd/lasso@latest`

Verify: `lasso version`

## Step 1 — assess state

```bash
lasso doctor --format json
```

- `meta.ready: true` → skip to step 4 if `enrollment` is `ok`.
- `config_file` missing → step 2.
- `application_id` / `mtls_*` missing → step 2.
- `enrollment` missing → step 3.

Doctor exits `4` when not ready; that is expected mid-setup, not a crash.

## Step 2 — write config

```bash
lasso init   # creates ~/.lasso/config.env (chmod 600); --force to overwrite
```

The config is a plain `KEY=VALUE` file. Edit it directly:

```text
TELLER_APPLICATION_ID=app_xxxxxxxx
TELLER_ENV=sandbox
# Required when TELLER_ENV is development or production:
TELLER_CERT_PATH=~/teller/certificate.pem
TELLER_KEY_PATH=~/teller/private_key.pem
```

Keep the file `0600`. Re-run doctor until only `enrollment` is missing.

## Step 3 — connect a bank (requires the human)

Bank linking happens in a browser via Teller Connect; an agent cannot
complete it alone. Run:

```bash
lasso connect --no-open --timeout 30m --format json
```

The command immediately prints a one-line JSON event on stdout:

```json
{"event":"connect.url","url":"http://127.0.0.1:8765/","expires_in_seconds":1800}
```

then **blocks** until the human finishes (run it in the background or a
separate terminal if you need to keep working). Relay the URL to the human
over whatever channel you share (Telegram, Slack, etc.) with instructions:

> Open this link in a browser **on the same machine the CLI is running
> on**, click "Open Teller Connect", and log in to your bank.
> (Sandbox credentials: username `username`, password `password`.)

If the CLI runs on a remote/headless machine, have the human port-forward
first: `ssh -L 8765:127.0.0.1:8765 <host>` then open the URL locally.

On success the command prints the final envelope (`"command": "connect"`)
with the enrollment metadata, and saves the access token to
`~/.lasso/enrollment.json` (chmod 600). The token is never printed.

## Step 4 — verify and sync

```bash
lasso accounts --format json       # proves the enrollment works end to end
lasso sync run --format json       # caches accounts/balances/transactions
lasso cache status --format json   # confirm counts and last sync
```

First transaction fetches on large accounts can be slow; the client
retries retryable Teller errors automatically.

## Day-to-day commands

```bash
lasso balance list --format json
lasso transaction list --since 30d --format json
lasso transaction search "amazon" --since 90d --format json
lasso spend summary --group merchant --since month --format json
lasso cashflow summary --since 6mo --format json
lasso transaction export --format csv --out tx.csv
```

Sign convention: `spend`/`merchant top`/`cashflow` normalize amounts per
account type so positive always means money out.

## Failure modes

| `error.code` | Meaning | What to do |
| --- | --- | --- |
| `config_error` | Config or enrollment file missing/invalid | Re-run the relevant setup step (`error.fix` says which) |
| `not_ready` | Doctor found missing setup | Fix the first `missing` check in `data` |
| `auth_error` | Token missing/revoked/wrong cert | Re-run step 3 (`lasso connect`) |
| `enrollment_disconnected` | Bank requires human action (MFA, re-login, captcha) | Tell the human to log in to their bank, then re-run `lasso connect` |
| `rate_limited` | Teller 429 | Back off and retry later (`retryable: true`) |
| `upstream_unavailable` | Institution down (502/504) | Retry later (`retryable: true`) |
| `network_error` | Request never completed | Check connectivity, retry |
| `not_found` | Unknown account/resource | Re-check IDs via `lasso account list` |
| `usage_error` | Bad flags/arguments | Consult `lasso schema <command>` |

Exit codes: `0` success, `1` general, `2` usage, `3` not found,
`4` auth/config, `5` conflict, `6` upstream unavailable, `7` retryable
network error.

## Security rules for agents

- Never print, log, or relay the Teller access token, cert, or key
  contents. `whoami` already redacts the token — use it freely.
- Config and enrollment files must stay `0600` (doctor warns otherwise).
- Everything is read-only against the bank; only the local cache and
  config are ever written.
