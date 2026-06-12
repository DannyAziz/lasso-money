# Agent-first CLI alignment plan

This plan shifts `lasso` from a human-friendly finance CLI toward a Stripe Link CLI-style tool surface for agents.

## Reference pattern

Stripe Link CLI patterns to emulate:

- Agent/non-TTY friendly output by default.
- `--format` supporting machine and LLM-friendly formats.
- Command discovery optimized for agents (`--llms`, `--llms-full`).
- Per-command schema output (`--schema`).
- Local MCP mode later, but CLI remains useful without MCP.
- Secure file output for sensitive values; stdout remains redacted.
- Credentials/session path override for multiple identities.
- Clear auth/status commands.

## Product principle

`lasso` should be a local finance data API over shell.

Agents are the primary users. Human-readable text is optional. Stable structured output is the product contract.

## Naming

Use `connection`, not `profile`.

A connection means:

```text
one Teller enrollment + one local cache + institution/account metadata
```

Canonical selector:

```bash
--connection <id>
```

Avoid `--profile` in docs. Avoid `--bank` as the canonical flag, because a Teller enrollment is not always exactly one bank and agents need exact resource names.

## Filesystem shape

```text
~/.lasso/
  config.env                  # app-level Teller config: app id, env, cert/key paths
  connections.json            # non-secret registry/current connection
  connections/
    amex/
      enrollment.json          # secret access token, 0600
      lasso.db
    chase/
      enrollment.json
      lasso.db
```

`connections.json` should contain only non-secret metadata:

```json
{
  "current": "amex",
  "connections": {
    "amex": {
      "id": "amex",
      "institution_id": "amex",
      "institution_name": "American Express",
      "enrollment_id": "enr_...",
      "created_at": "2026-06-12T00:00:00Z",
      "last_sync_at": "2026-06-12T00:00:00Z"
    }
  }
}
```

## Output contract

### Formats

Global:

```bash
--format json|jsonl|text|csv
```

Recommended default for agent/non-TTY use:

```text
json
```

Human text remains available:

```bash
--format text
```

Large row streams should support:

```bash
--format jsonl
```

### Stdout/stderr

- stdout: data only.
- stderr: progress, warnings, hints, update notices.
- Never mix prose into JSON stdout.

### Envelope

All JSON command output should use a consistent envelope:

```json
{
  "ok": true,
  "schema_version": "2026-06-12",
  "command": "transaction.list",
  "connection_id": "amex",
  "data": [],
  "meta": {
    "count": 0,
    "source": "cache",
    "truncated": false
  },
  "warnings": [],
  "next_actions": []
}
```

Error envelope:

```json
{
  "ok": false,
  "schema_version": "2026-06-12",
  "command": "connection.get",
  "error": {
    "code": "connection_not_found",
    "message": "No connection exists with id 'chase'.",
    "retryable": false,
    "fix": "Run: lasso connection create --id chase --institution chase"
  },
  "warnings": [],
  "next_actions": [
    {
      "command": "lasso connection list --format json",
      "description": "List existing connections"
    }
  ]
}
```

## Exit codes

```text
0 success
1 general error
2 usage/validation error
3 not found
4 auth/config/permission error
5 conflict/already exists
6 upstream/Teller unavailable
7 retryable network error
```

## Discovery affordances

Root discovery:

```bash
lasso --llms
lasso --llms-full
```

`--llms` should return a compact command index with examples and safety notes.

`--llms-full` should return a fuller agent guide, including schemas/examples for common workflows.

Schema discovery:

```bash
lasso schema
lasso schema transaction.list
lasso transaction list --schema
```

Schema output should be JSON and include:

- command name
- description
- input schema
- output schema summary
- examples
- side_effect level: `read_cache`, `read_live`, `write_local`, `remote_revoke`
- sensitive fields and redaction policy

## Canonical command tree

Keep old aliases, but document these as canonical:

```text
lasso
  doctor
  schema
  institution
    list
    search
    get
  connection
    list
    get
    create
    connect
    reconnect
    use
    status
    remove
    unlink
  account
    list
    get
  balance
    list
  transaction
    list
    get
    search
    export
  sync
    run
    status
  spend
    summary
  merchant
    top
  cashflow
    summary
  cache
    status
    path
    clear
    vacuum
```

Alias mapping:

```text
accounts          -> account list
balances          -> balance list
tx                -> transaction list
search            -> transaction search
export tx         -> transaction export
spend             -> spend summary
merchants top     -> merchant top
cashflow          -> cashflow summary
sync              -> sync run
cache status      -> cache status
```

## Common agent workflows

### Discover capabilities

```bash
lasso --llms
lasso schema transaction.list
```

### Add a connection

```bash
lasso institution search --query chase --format json
lasso connection create --id chase --institution chase --format json
lasso connection connect --connection chase --format json
```

### Sync and query

```bash
lasso sync run --connection amex --format json
lasso transaction search --connection amex --query amazon --since ytd --format json
```

### All-connection analysis

```bash
lasso sync run --all --format json
lasso spend summary --all --group merchant --since ytd --format json
```

## Token efficiency

List/table-like commands should support:

```bash
--fields id,date,amount,counterparty_name,status
--limit 100
--cursor <cursor>
```

For transaction streams:

```bash
--format jsonl
```

## Security policy

- Enrollment/access token is never printed.
- Cert/key values are never printed.
- Account numbers/routing numbers require explicit command and should default to redacted output.
- Any remote revoke/unlink requires explicit confirmation unless `--yes` is provided.
- Read-only default remains strict: no payments, no card controls, no settings changes.

## Implementation phases

### Phase 1: Output and discovery

- Add global `--format`.
- Make JSON envelope available for core commands.
- Add `--llms`, `--llms-full`.
- Add `schema` and per-command `--schema` for core commands.
- Keep existing text output behind `--format text`.

### Phase 2: Canonical resource commands

- Add `account list`, `balance list`, `transaction list/search/export`, `spend summary`, `merchant top`, `cashflow summary`.
- Keep existing aliases.
- Improve all subcommand help.

### Phase 3: Connections

- Add connection registry and `--connection`.
- Add `connection list/get/create/use/status`.
- Move enrollment/db path resolution under connection directories.
- Add `institution search/list/get` via Teller institutions API.

### Phase 4: Agent ergonomics

- Add `--fields`, `--limit`, cursor/truncation metadata.
- Add semantic exit codes and structured JSON errors.
- Add contextual `next_actions`.

### Phase 5: Optional MCP

- Add local MCP mode after CLI contract is stable.
- Generate MCP tools from the same command/schema registry rather than duplicating logic.
