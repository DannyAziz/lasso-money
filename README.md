# Lasso Money

A local-first, read-only command-line tool for exploring your bank data — and giving AI agents a safe, structured way to do the same.

Lasso connects to your bank through [Teller](https://teller.io), stores a local SQLite cache, and lets you search transactions, summarize spending, inspect cash flow, and export your data.

> **Early release:** Lasso currently requires a Teller account.

## What you can do

```bash
lasso accounts
lasso balances
lasso sync
lasso tx --since 30d
lasso search "amazon" --since 90d
lasso spend --group merchant --since month
lasso cashflow --since 6mo
lasso export tx --since month --format csv --out transactions.csv
```

Lasso only reads from Teller. Your configuration, enrollment token, and local account, balance, transaction, and sync metadata cache stay in `~/.lasso` on your machine.

## Install

### 1. Install Lasso

On macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/DannyAziz/lasso-money/main/install.sh | sh
```

The installer downloads a precompiled binary, verifies its checksum, and installs it to `/usr/local/bin` or `~/.local/bin`. **Go is not required.**

Verify the installation:

```bash
lasso version
```

If the installer says `~/.local/bin` is not on your `PATH`, follow the command it prints.

Alternatively, if you already have [Go 1.26 or newer](https://go.dev/doc/install):

```bash
go install github.com/dannyaziz/lasso-money/cmd/lasso@latest
```

### 2. Get your Teller application ID

1. Sign up at [teller.io](https://teller.io) and open the [dashboard](https://teller.io/dashboard).
2. Open **Settings → Application** and copy the application ID (`app_...`). Every account already has one application.
3. Choose an environment:
   - **Sandbox** uses fake bank data and requires no certificates. Start here if you are trying Lasso for the first time.
   - **Development** or **production** uses real bank data. Under **Settings → Certificates**, create a certificate and download the mTLS certificate and private key.

### 3. Configure Lasso

Create the config file:

```bash
lasso init
```

Open `~/.lasso/config.env` and add your Teller details.

For sandbox:

```text
TELLER_APPLICATION_ID=app_your_application_id
TELLER_ENV=sandbox
```

For real bank data:

```text
TELLER_APPLICATION_ID=app_your_application_id
TELLER_ENV=development
TELLER_CERT_PATH=/full/path/to/certificate.pem
TELLER_KEY_PATH=/full/path/to/private_key.pem
```

Check your setup:

```bash
lasso doctor
```

Follow its suggested fix until it reports `ready`.

### 4. Link your bank

```bash
lasso connect
```

Your browser will open Teller Connect. Complete the bank login there; Lasso never asks you to paste your banking credentials into the terminal.

For Teller's sandbox, use username `username` and password `password`.

### 5. Sync your data

```bash
lasso sync
lasso cache status
```

You are ready to use Lasso.

## Everyday use

```bash
# See live accounts and balances (balances are cached for five minutes)
lasso accounts
lasso balances
lasso balances --live  # force a refresh

# Refresh the local cache
lasso sync --since 90d

# Browse and search transactions
lasso tx --since 30d
lasso search "coffee" --since 90d

# Understand spending and cash flow
lasso spend --group merchant --since month
lasso merchants top --since 90d
lasso cashflow --since 6mo

# Export your data
lasso export tx --since ytd --format csv --out transactions.csv
```

Run `lasso --help` for all commands and flags.

## Use with an AI agent

Lasso provides command schemas and stable JSON output for agents:

```bash
lasso --llms
lasso schema
lasso tx --since 30d --format json
lasso spend --group merchant --since month --format json
```

If an agent is installing Lasso for you, give it [SETUP.md](SETUP.md). Bank linking still requires you to complete Teller Connect in a browser.

## Where your data lives

Lasso writes only to local files:

```text
~/.lasso/config.env       Teller application settings
~/.lasso/enrollment.json Teller enrollment token
~/.lasso/lasso.db        SQLite account, balance, transaction, and sync cache
```

The config and enrollment files are created with owner-only permissions. Do not commit or share them. `lasso whoami` shows enrollment details with the access token redacted.

## Troubleshooting

Start with:

```bash
lasso doctor
```

Common fixes:

- **`lasso: command not found`** — add `$(go env GOPATH)/bin` to your `PATH`.
- **Missing application ID** — add `TELLER_APPLICATION_ID` to `~/.lasso/config.env`.
- **Missing certificate or key** — real-data environments require both Teller mTLS files and their full paths.
- **Enrollment or authentication error** — run `lasso connect` again.
- **Bank requires MFA or re-login** — complete that action with your bank, then reconnect.

For machine-readable errors and suggested next actions, add `--format json`.

## Build from source

```bash
git clone https://github.com/DannyAziz/lasso-money.git
cd lasso-money
go test ./...
go build -o ./bin/lasso ./cmd/lasso
./bin/lasso --help
```

## License

MIT — see [LICENSE](LICENSE).
