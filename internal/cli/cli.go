package cli

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dannyaziz/lasso-money/internal/config"
	"github.com/dannyaziz/lasso-money/internal/connect"
	"github.com/dannyaziz/lasso-money/internal/store"
	"github.com/dannyaziz/lasso-money/internal/teller"
)

const Version = "0.1.0-dev"

type App struct {
	Out    io.Writer
	Err    io.Writer
	Format string
}

const schemaVersion = "2026-06-12"

type envelope struct {
	OK            bool             `json:"ok"`
	SchemaVersion string           `json:"schema_version"`
	Command       string           `json:"command"`
	ConnectionID  string           `json:"connection_id,omitempty"`
	Data          any              `json:"data,omitempty"`
	Meta          map[string]any   `json:"meta,omitempty"`
	Warnings      []string         `json:"warnings"`
	NextActions   []nextAction     `json:"next_actions"`
	Error         *structuredError `json:"error,omitempty"`
}

type nextAction struct {
	Command     string            `json:"command"`
	Description string            `json:"description,omitempty"`
	Params      map[string]string `json:"params,omitempty"`
}

type structuredError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Fix       string `json:"fix,omitempty"`
}

type runtimeState struct {
	Paths          config.Paths
	Env            config.Env
	EnrollmentPath string
	Enrollment     teller.Enrollment
	Client         *teller.Client
}

func Run(args []string) error {
	app := App{Out: os.Stdout, Err: os.Stderr}
	return app.Run(args)
}

func (a App) Run(args []string) error {
	args, a.Format = parseGlobalArgs(args, a.Format)
	if len(args) == 0 {
		a.printHelp(a.Out)
		return nil
	}
	if hasSchemaFlag(args) {
		return a.schema(schemaArgsFromCommand(args))
	}

	switch args[0] {
	case "--llms", "llms":
		return a.printLLMS(false)
	case "--llms-full", "llms-full":
		return a.printLLMS(true)
	case "schema":
		return a.schema(args[1:])
	case "help", "--help", "-h":
		a.printHelp(a.Out)
		return nil
	case "version", "--version", "-v":
		fmt.Fprintf(a.Out, "lasso %s\n", Version)
		return nil
	case "init":
		return a.init(args[1:])
	case "doctor":
		return a.doctor(args[1:])
	case "connect":
		return a.connect(args[1:])
	case "whoami":
		return a.whoami(args[1:])
	case "accounts":
		return a.accounts(args[1:])
	case "account":
		return a.account(args[1:])
	case "tx", "transactions":
		return a.transactions(args[1:])
	case "transaction":
		return a.transaction(args[1:])
	case "balances":
		return a.balances(args[1:])
	case "balance":
		return a.balance(args[1:])
	case "sync":
		return a.syncCommand(args[1:])
	case "search":
		return a.search(args[1:])
	case "spend":
		return a.spendCommand(args[1:])
	case "merchants":
		return a.merchants(args[1:])
	case "merchant":
		return a.merchant(args[1:])
	case "cashflow":
		return a.cashflowCommand(args[1:])
	case "export":
		return a.export(args[1:])
	case "cache":
		return a.cache(args[1:])
	default:
		return fmt.Errorf("unknown command %q\n\nrun `lasso help` for usage", args[0])
	}
}

func (a App) printHelp(w io.Writer) {
	fmt.Fprint(w, strings.TrimSpace(`lasso is a local-first read-only finance CLI powered by Teller.

Usage:
  lasso <command> [flags]

Commands:
  --llms      Print compact agent guide
  schema      Print command schemas for agents
  init        Create local config template
  doctor      Check local Teller configuration without printing secrets
  connect     Launch Teller Connect and save enrollment locally
  whoami      Print saved enrollment metadata with access token redacted
  account     Canonical resource command: account list
  accounts    List linked Teller accounts
  balance     Canonical resource command: balance list
  balances    Show live Teller balances
  transaction Canonical resource command: transaction list/search/export
  tx          List transactions from cache, or live with --live
  sync        Sync accounts/balances/transactions into local SQLite
  search      Search cached transactions
  spend       Summarize cached spending
  merchant    Canonical resource command: merchant top
  merchants   Show top cached merchants
  cashflow    Show monthly cached inflow/outflow/net
  export      Export cached transactions
  cache       Inspect local cache
  version     Print version
  help        Show this help

Agent output:
  Use --format json for a stable envelope: ok, schema_version, command, data, meta, warnings, next_actions.
  Legacy --json flags still emit raw arrays/objects for backwards compatibility.
`))
	fmt.Fprintln(w)
}

func parseGlobalArgs(args []string, current string) ([]string, string) {
	format := current
	if format == "" {
		format = os.Getenv("LASSO_FORMAT")
	}
	// `export tx --format csv|json|jsonl` already uses --format for file format.
	// Do not steal that flag as the envelope selector.
	if len(args) > 0 && (args[0] == "export" || (len(args) > 1 && args[0] == "transaction" && args[1] == "export")) {
		return args, format
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--format" && i+1 < len(args) {
			format = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(arg, "--format=") {
			format = strings.TrimPrefix(arg, "--format=")
			continue
		}
		out = append(out, arg)
	}
	return out, format
}

func (a App) envelopeJSON() bool { return a.Format == "json" }

func hasSchemaFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--schema" {
			return true
		}
	}
	return false
}

func schemaArgsFromCommand(args []string) []string {
	clean := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != "--schema" {
			clean = append(clean, arg)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	if len(clean) >= 2 {
		switch clean[0] + " " + clean[1] {
		case "account list":
			return []string{"account.list"}
		case "balance list":
			return []string{"balance.list"}
		case "sync run":
			return []string{"sync.run"}
		case "transaction list":
			return []string{"transaction.list"}
		case "transaction search":
			return []string{"transaction.search"}
		case "transaction export":
			return []string{"transaction.export"}
		case "spend summary":
			return []string{"spend.summary"}
		case "merchant top":
			return []string{"merchant.top"}
		case "cashflow summary":
			return []string{"cashflow.summary"}
		case "cache status":
			return []string{"cache.status"}
		}
	}
	switch clean[0] {
	case "accounts":
		return []string{"account.list"}
	case "balances":
		return []string{"balance.list"}
	case "tx", "transactions":
		return []string{"transaction.list"}
	case "search":
		return []string{"transaction.search"}
	case "spend":
		return []string{"spend.summary"}
	case "cashflow":
		return []string{"cashflow.summary"}
	}
	return clean[:1]
}

func (a App) writeEnvelope(command string, data any, meta map[string]any, next []nextAction) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(envelope{OK: true, SchemaVersion: schemaVersion, Command: command, Data: data, Meta: meta, Warnings: []string{}, NextActions: next})
}

func (a App) writeRows(command string, rows any, count int, source string, next []nextAction) error {
	return a.writeEnvelope(command, rows, map[string]any{"count": count, "source": source, "truncated": false}, next)
}

func (a App) printLLMS(full bool) error {
	guide := map[string]any{
		"name":            "lasso",
		"purpose":         "Local-first read-only Teller finance CLI for agents.",
		"format":          "Use --format json for stable envelopes. Legacy --json emits raw data.",
		"security":        "Never prints Teller access tokens, cert contents, key contents, or full account numbers by default.",
		"connection_flag": "--connection <id> is planned as the canonical selector; current MVP uses config/enrollment path overrides.",
		"canonical_commands": []string{
			"lasso schema",
			"lasso account list --format json",
			"lasso balance list --format json",
			"lasso sync run --format json",
			"lasso transaction list --since ytd --merchant amazon --format json",
			"lasso transaction search amazon --since ytd --format json",
			"lasso spend summary --group merchant --since month --format json",
			"lasso merchant top --since 90d --format json",
			"lasso cashflow summary --since 6mo --format json",
			"lasso cache status --format json",
		},
	}
	if full {
		guide["aliases"] = map[string]string{"accounts": "account list", "balances": "balance list", "tx": "transaction list", "search": "transaction search", "export tx": "transaction export", "spend": "spend summary", "merchants top": "merchant top", "cashflow": "cashflow summary", "sync": "sync run"}
		guide["exit_codes"] = map[string]string{"0": "success", "1": "general error", "2": "usage/validation error", "3": "not found", "4": "auth/config/permission error", "5": "conflict", "6": "upstream unavailable", "7": "retryable network error"}
	}
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(guide)
}

func (a App) schema(args []string) error {
	commands := commandSchemas()
	if len(args) > 0 {
		name := strings.Join(args, ".")
		if s, ok := commands[name]; ok {
			enc := json.NewEncoder(a.Out)
			enc.SetIndent("", "  ")
			return enc.Encode(s)
		}
		return fmt.Errorf("unknown schema %q", name)
	}
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"schema_version": schemaVersion, "commands": commands})
}

func commandSchemas() map[string]any {
	return map[string]any{
		"account.list":       schemaEntry("account.list", "List live Teller accounts", []string{"--config"}),
		"balance.list":       schemaEntry("balance.list", "List live Teller balances", []string{"--config"}),
		"sync.run":           schemaEntry("sync.run", "Sync Teller accounts, balances, and transactions into local SQLite", []string{"--config", "--account", "--since", "--from", "--to"}),
		"transaction.list":   schemaEntry("transaction.list", "List cached transactions, or live with --live", []string{"--config", "--account", "--since", "--from", "--to", "--merchant", "--category", "--pending", "--posted", "--limit", "--live"}),
		"transaction.search": schemaEntry("transaction.search", "Search cached transactions", []string{"query", "--config", "--since", "--merchant", "--category", "--pending", "--posted", "--limit"}),
		"transaction.export": schemaEntry("transaction.export", "Export cached transactions", []string{"--format csv|json|jsonl", "--out", "--since", "--status", "--merchant", "--category"}),
		"spend.summary":      schemaEntry("spend.summary", "Summarize cached spending", []string{"--group merchant|category|account|month", "--since", "--from", "--to"}),
		"merchant.top":       schemaEntry("merchant.top", "Show top cached merchants", []string{"--since", "--from", "--to"}),
		"cashflow.summary":   schemaEntry("cashflow.summary", "Show monthly cached inflow/outflow/net", []string{"--since", "--from", "--to"}),
		"cache.status":       schemaEntry("cache.status", "Inspect local cache", []string{"--config"}),
	}
}

func schemaEntry(name, description string, flags []string) map[string]any {
	return map[string]any{"name": name, "description": description, "flags": flags, "output": "Use --format json for envelope output", "side_effect": sideEffect(name)}
}

func sideEffect(name string) string {
	if strings.HasPrefix(name, "sync.") {
		return "read_live_write_local_cache"
	}
	if strings.HasPrefix(name, "account.") || strings.HasPrefix(name, "balance.") {
		return "read_live"
	}
	return "read_cache"
}

func (a App) account(args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return fmt.Errorf("usage: lasso account list [--format json]")
	}
	return a.accounts(args[1:])
}

func (a App) balance(args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return fmt.Errorf("usage: lasso balance list [--format json]")
	}
	return a.balances(args[1:])
}

func (a App) transaction(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: lasso transaction list|search|export")
	}
	switch args[0] {
	case "list":
		return a.transactions(args[1:])
	case "search":
		return a.search(args[1:])
	case "export":
		return a.export(append([]string{"tx"}, args[1:]...))
	default:
		return fmt.Errorf("usage: lasso transaction list|search|export")
	}
}

func (a App) syncCommand(args []string) error {
	if len(args) > 0 && args[0] == "run" {
		args = args[1:]
	}
	if len(args) > 0 && args[0] == "status" {
		return a.cache([]string{"status"})
	}
	return a.sync(args)
}

func (a App) spendCommand(args []string) error {
	if len(args) > 0 && args[0] == "summary" {
		args = args[1:]
	}
	return a.spend(args)
}

func (a App) merchant(args []string) error {
	if len(args) == 0 || args[0] != "top" {
		return fmt.Errorf("usage: lasso merchant top [--format json]")
	}
	return a.merchants(args)
}

func (a App) cashflowCommand(args []string) error {
	if len(args) > 0 && args[0] == "summary" {
		args = args[1:]
	}
	return a.cashflow(args)
}

func (a App) init(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path; defaults to ~/.lasso/config.env")
	force := fs.Bool("force", false, "overwrite an existing config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	paths, err := config.ResolvePaths(*configPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(paths.ConfigFile); err == nil && !*force {
		return fmt.Errorf("config already exists at %s; pass --force to overwrite", paths.ConfigFile)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
		return err
	}
	body := strings.TrimSpace(`# Teller application ID from https://dashboard.teller.io
TELLER_APPLICATION_ID=

# sandbox | development | production
TELLER_ENV=sandbox

# Required for development/production. Keep these outside repos.
TELLER_CERT_PATH=
TELLER_KEY_PATH=

# Optional: override where the local SQLite cache is stored.
# TELLER_DB_PATH=~/.lasso/lasso.db

# Optional: override where the Teller Connect enrollment token is stored.
# TELLER_ENROLLMENT_PATH=~/.lasso/enrollment.json
`) + "\n"
	if err := os.WriteFile(paths.ConfigFile, []byte(body), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "wrote %s\n", paths.ConfigFile)
	fmt.Fprintln(a.Out, "edit it, then run `lasso doctor`")
	return nil
}

func (a App) doctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path; defaults to ~/.lasso/config.env")
	if err := fs.Parse(args); err != nil {
		return err
	}

	paths, err := config.ResolvePaths(*configPath)
	if err != nil {
		return err
	}

	cfg, cfgErr := config.LoadOptionalEnvFile(paths.ConfigFile)
	enrollmentPath := config.ResolveEnrollmentPath(paths, cfg)

	fmt.Fprintln(a.Out, "lasso doctor")
	fmt.Fprintf(a.Out, "config: %s\n", paths.ConfigFile)
	fmt.Fprintf(a.Out, "data_dir: %s\n", paths.DataDir)
	fmt.Fprintf(a.Out, "enrollment_file: %s\n", enrollmentPath)
	fmt.Fprintf(a.Out, "db_file: %s\n", config.ExpandHome(cfg.GetDefault("TELLER_DB_PATH", paths.DBFile)))
	fmt.Fprintln(a.Out)

	ok := true
	if cfgErr != nil {
		ok = false
		fmt.Fprintf(a.Out, "[missing] config file: %v\n", cfgErr)
		fmt.Fprintln(a.Out, "          run `lasso init`")
	} else {
		fmt.Fprintln(a.Out, "[ok] config file exists")
		checkFileMode(a.Out, paths.ConfigFile)
	}

	if cfgErr == nil {
		ok = checkEnvPresent(a.Out, cfg, "TELLER_APPLICATION_ID") && ok
		env := cfg.GetDefault("TELLER_ENV", "sandbox")
		fmt.Fprintf(a.Out, "[ok] TELLER_ENV=%s\n", env)

		if env != "sandbox" {
			ok = checkEnvPresent(a.Out, cfg, "TELLER_CERT_PATH") && ok
			ok = checkEnvPresent(a.Out, cfg, "TELLER_KEY_PATH") && ok
			ok = checkPathIfPresent(a.Out, cfg.Get("TELLER_CERT_PATH"), "TELLER_CERT_PATH") && ok
			ok = checkPathIfPresent(a.Out, cfg.Get("TELLER_KEY_PATH"), "TELLER_KEY_PATH") && ok
		} else {
			fmt.Fprintln(a.Out, "[ok] sandbox mode does not require mTLS cert/key")
		}
	}

	if _, err := os.Stat(enrollmentPath); err != nil {
		ok = false
		fmt.Fprintf(a.Out, "[missing] enrollment file: %v\n", err)
		fmt.Fprintln(a.Out, "         run `lasso connect` once implemented")
	} else {
		fmt.Fprintln(a.Out, "[ok] enrollment file exists")
		checkFileMode(a.Out, enrollmentPath)
	}

	if !ok {
		return fmt.Errorf("doctor found missing required setup")
	}
	fmt.Fprintln(a.Out, "ready")
	return nil
}

func (a App) connect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	port := fs.Int("port", 0, "local port; default tries 8765 then a random port")
	timeout := fs.Duration("timeout", 5*time.Minute, "how long to wait for Teller Connect")
	noOpen := fs.Bool("no-open", false, "print URL instead of opening browser")
	if err := fs.Parse(args); err != nil {
		return err
	}

	paths, err := config.ResolvePaths(*configPath)
	if err != nil {
		return err
	}
	cfg, err := config.LoadEnvFile(paths.ConfigFile)
	if err != nil {
		return fmt.Errorf("load config %s: %w", paths.ConfigFile, err)
	}
	enrollmentPath := config.ResolveEnrollmentPath(paths, cfg)
	if cfg.Get("TELLER_APPLICATION_ID") == "" {
		return fmt.Errorf("TELLER_APPLICATION_ID is required; edit %s", paths.ConfigFile)
	}
	enrollment, err := connect.Run(context.Background(), connect.Options{
		ApplicationID:  cfg.Get("TELLER_APPLICATION_ID"),
		Environment:    cfg.GetDefault("TELLER_ENV", "sandbox"),
		Port:           *port,
		Timeout:        *timeout,
		OpenBrowser:    !*noOpen,
		EnrollmentPath: enrollmentPath,
		Status: func(message string) {
			fmt.Fprintln(a.Out, message)
		},
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "linked %s (%s)\n", fallback(enrollment.InstitutionName, "institution"), enrollment.ID)
	fmt.Fprintf(a.Out, "saved enrollment: %s\n", enrollmentPath)
	return nil
}

func (a App) whoami(args []string) error {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	state, err := loadState(*configPath, false)
	if err != nil {
		return err
	}
	out := map[string]any{
		"id":               state.Enrollment.ID,
		"institution_id":   state.Enrollment.InstitutionID,
		"institution_name": state.Enrollment.InstitutionName,
		"provider":         state.Enrollment.Provider,
		"access_token":     teller.MaskToken(state.Enrollment.AccessToken),
		"path":             state.EnrollmentPath,
	}
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func (a App) accounts(args []string) error {
	fs := flag.NewFlagSet("accounts", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	state, err := loadState(*configPath, true)
	if err != nil {
		return err
	}
	accounts, err := state.Client.ListAccounts(state.Enrollment)
	if err != nil {
		return explainTellerError(err)
	}
	if a.envelopeJSON() {
		return a.writeRows("account.list", accounts, len(accounts), "live", []nextAction{{Command: "lasso sync run --format json", Description: "Cache live account, balance, and transaction data"}})
	}
	if *jsonOut {
		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(accounts)
	}
	for _, account := range accounts {
		fmt.Fprintf(a.Out, "%s\t%s/%s\t%s\t••%s\t%s\n", account.ID, account.Type, account.Subtype, account.Name, fallback(account.LastFour, "????"), account.Status)
	}
	return nil
}

func (a App) transactions(args []string) error {
	fs := flag.NewFlagSet("tx", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	jsonOut := fs.Bool("json", false, "emit JSON")
	live := fs.Bool("live", false, "fetch live from Teller instead of local cache")
	accountSelector := fs.String("account", "", "account id, last four, or name substring")
	from := fs.String("from", "", "start date YYYY-MM-DD")
	to := fs.String("to", "", "end date YYYY-MM-DD; defaults to today")
	since := fs.String("since", "30d", "relative window: 7d, 30d, 90d, month")
	limit := fs.Int("limit", 50, "max rows to print")
	pending := fs.Bool("pending", false, "only pending transactions")
	posted := fs.Bool("posted", false, "only posted transactions")
	allStatuses := fs.Bool("all", false, "include all statuses; default")
	minAmount := fs.String("min", "", "minimum amount")
	maxAmount := fs.String("max", "", "maximum amount")
	category := fs.String("category", "", "category substring")
	merchant := fs.String("merchant", "", "merchant/counterparty substring")
	if err := fs.Parse(args); err != nil {
		return err
	}
	startDate, endDate, err := dateWindow(*from, *to, *since)
	if err != nil {
		return err
	}
	if !*live {
		status, err := statusFilter(*pending, *posted, *allStatuses)
		if err != nil {
			return err
		}
		if err := validateAmountFilter(*minAmount, "--min"); err != nil {
			return err
		}
		if err := validateAmountFilter(*maxAmount, "--max"); err != nil {
			return err
		}
		return a.cachedTransactions(*configPath, *accountSelector, store.TxFilter{From: startDate, To: endDate, Status: status, MinAmount: *minAmount, MaxAmount: *maxAmount, Category: *category, Merchant: *merchant, Limit: *limit}, *jsonOut)
	}
	state, err := loadState(*configPath, true)
	if err != nil {
		return err
	}
	accounts, err := state.Client.ListAccounts(state.Enrollment)
	if err != nil {
		return explainTellerError(err)
	}
	account, err := selectAccount(accounts, *accountSelector)
	if err != nil {
		return err
	}
	txs, err := state.Client.ListTransactions(state.Enrollment, account.ID, startDate, endDate, 500)
	if err != nil {
		return explainTellerError(err)
	}
	if a.envelopeJSON() {
		return a.writeRows("transaction.list", txs, len(txs), "live", []nextAction{{Command: "lasso sync run --format json", Description: "Cache transactions locally for repeat analysis"}})
	}
	if *jsonOut {
		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(txs)
	}
	max := *limit
	if max <= 0 || max > len(txs) {
		max = len(txs)
	}
	fmt.Fprintf(a.Out, "%s → %s  %s  %d transactions\n", startDate, endDate, account.Name, len(txs))
	for _, tx := range txs[:max] {
		name := tx.Description
		if detailsName := counterpartyName(tx.Details); detailsName != "" {
			name = detailsName
		}
		fmt.Fprintf(a.Out, "%s\t%10s\t%-8s\t%s\n", tx.Date, tx.Amount, tx.Status, name)
	}
	if max < len(txs) {
		fmt.Fprintf(a.Out, "… %d more; use --limit or --json\n", len(txs)-max)
	}
	return nil
}

func (a App) balances(args []string) error {
	fs := flag.NewFlagSet("balances", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	state, err := loadState(*configPath, true)
	if err != nil {
		return err
	}
	accounts, err := state.Client.ListAccounts(state.Enrollment)
	if err != nil {
		return explainTellerError(err)
	}
	type row struct {
		AccountID string `json:"account_id"`
		Name      string `json:"name"`
		LastFour  string `json:"last_four,omitempty"`
		Currency  string `json:"currency,omitempty"`
		Ledger    string `json:"ledger,omitempty"`
		Available string `json:"available,omitempty"`
	}
	rows := make([]row, 0, len(accounts))
	for _, account := range accounts {
		balance, err := state.Client.GetBalance(state.Enrollment, account.ID)
		if err != nil {
			return explainTellerError(err)
		}
		rows = append(rows, row{AccountID: account.ID, Name: account.Name, LastFour: account.LastFour, Currency: account.Currency, Ledger: balance.Ledger, Available: balance.Available})
	}
	if a.envelopeJSON() {
		return a.writeRows("balance.list", rows, len(rows), "live", []nextAction{{Command: "lasso sync run --format json", Description: "Refresh local cache"}})
	}
	if *jsonOut {
		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	for _, r := range rows {
		fmt.Fprintf(a.Out, "%s\t%s\t••%s\tledger=%s\tavailable=%s\t%s\n", r.AccountID, r.Name, fallback(r.LastFour, "????"), fallback(r.Ledger, "—"), fallback(r.Available, "—"), r.Currency)
	}
	return nil
}

func (a App) sync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	accountSelector := fs.String("account", "", "account id, last four, or name substring")
	from := fs.String("from", "", "start date YYYY-MM-DD")
	to := fs.String("to", "", "end date YYYY-MM-DD; defaults to today")
	since := fs.String("since", "", "relative window: 7d, 30d, 90d, month, ytd; default incremental with 10d overlap")
	if err := fs.Parse(args); err != nil {
		return err
	}
	incremental := *from == "" && *since == ""
	startDate, endDate, err := dateWindow(*from, *to, *since)
	if err != nil {
		return err
	}
	state, err := loadState(*configPath, true)
	if err != nil {
		return err
	}
	db, err := openStore(state)
	if err != nil {
		return err
	}
	defer db.Close()

	accounts, err := state.Client.ListAccounts(state.Enrollment)
	if err != nil {
		return explainTellerError(err)
	}
	if err := db.UpsertAccounts(accounts); err != nil {
		return err
	}
	var selected []teller.Account
	if *accountSelector != "" {
		account, err := selectAccount(accounts, *accountSelector)
		if err != nil {
			return err
		}
		selected = []teller.Account{account}
	} else {
		selected = accounts
	}
	total := 0
	syncRows := make([]map[string]any, 0, len(selected))
	for _, account := range selected {
		accountStart := startDate
		if incremental {
			accountStart, err = db.IncrementalStartDate(account.ID, 10, 90)
			if err != nil {
				return err
			}
		}
		runID, _ := db.StartSyncRun(account.ID, accountStart, endDate)
		balance, berr := state.Client.GetBalance(state.Enrollment, account.ID)
		if berr == nil {
			_ = db.UpsertBalance(account, balance)
		}
		txs, err := state.Client.ListTransactions(state.Enrollment, account.ID, accountStart, endDate, 500)
		if err != nil {
			_ = db.FinishSyncRun(runID, "failed", 0, err.Error())
			return explainTellerError(err)
		}
		if err := db.UpsertTransactions(account, txs); err != nil {
			_ = db.FinishSyncRun(runID, "failed", len(txs), err.Error())
			return err
		}
		_ = db.FinishSyncRun(runID, "ok", len(txs), "")
		total += len(txs)
		syncRows = append(syncRows, map[string]any{"account_id": account.ID, "account_name": account.Name, "account_last_four": account.LastFour, "start_date": accountStart, "end_date": endDate, "transactions_synced": len(txs)})
		if !a.envelopeJSON() {
			fmt.Fprintf(a.Out, "synced %s ••%s %s→%s: %d transactions\n", account.Name, fallback(account.LastFour, "????"), accountStart, endDate, len(txs))
		}
	}
	if a.envelopeJSON() {
		return a.writeEnvelope("sync.run", syncRows, map[string]any{"accounts": len(syncRows), "transactions_synced": total, "cache_path": dbPath(state)}, []nextAction{{Command: "lasso cache status --format json", Description: "Inspect cache counts and last sync"}, {Command: "lasso transaction list --since 30d --format json", Description: "Query cached transactions"}})
	}
	fmt.Fprintf(a.Out, "cache: %s\n", dbPath(state))
	fmt.Fprintf(a.Out, "total transactions synced: %d\n", total)
	return nil
}

func (a App) cachedTransactions(configPath, accountSelector string, filter store.TxFilter, jsonOut bool) error {
	state, err := loadState(configPath, false)
	if err != nil {
		return err
	}
	db, err := openStore(state)
	if err != nil {
		return err
	}
	defer db.Close()
	if accountSelector != "" {
		accounts, err := cachedAccounts(db)
		if err != nil {
			return err
		}
		account, err := selectAccount(accounts, accountSelector)
		if err != nil {
			return err
		}
		filter.AccountID = account.ID
	}
	rows, err := db.QueryTransactions(filter)
	if err != nil {
		return err
	}
	if a.envelopeJSON() {
		return a.writeRows("transaction.list", rows, len(rows), "cache", []nextAction{{Command: "lasso spend summary --since month --format json", Description: "Summarize cached spend"}})
	}
	if jsonOut {
		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	fmt.Fprintf(a.Out, "%s → %s  %d cached transactions\n", filter.From, filter.To, len(rows))
	for _, tx := range rows {
		name := fallback(tx.CounterpartyName, tx.Description)
		fmt.Fprintf(a.Out, "%s\t%10s\t%-8s\t%s\n", tx.Date, tx.Amount, tx.Status, name)
	}
	return nil
}

func (a App) search(args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	jsonOut := fs.Bool("json", false, "emit JSON")
	from := fs.String("from", "", "start date YYYY-MM-DD")
	to := fs.String("to", "", "end date YYYY-MM-DD; defaults to today")
	since := fs.String("since", "90d", "relative window")
	limit := fs.Int("limit", 100, "max rows")
	pending := fs.Bool("pending", false, "only pending transactions")
	posted := fs.Bool("posted", false, "only posted transactions")
	allStatuses := fs.Bool("all", false, "include all statuses; default")
	minAmount := fs.String("min", "", "minimum amount")
	maxAmount := fs.String("max", "", "maximum amount")
	category := fs.String("category", "", "category substring")
	merchant := fs.String("merchant", "", "merchant/counterparty substring")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: lasso search <query> [--since 90d]")
	}
	startDate, endDate, err := dateWindow(*from, *to, *since)
	if err != nil {
		return err
	}
	status, err := statusFilter(*pending, *posted, *allStatuses)
	if err != nil {
		return err
	}
	if err := validateAmountFilter(*minAmount, "--min"); err != nil {
		return err
	}
	if err := validateAmountFilter(*maxAmount, "--max"); err != nil {
		return err
	}
	return a.cachedTransactions(*configPath, "", store.TxFilter{From: startDate, To: endDate, Query: strings.Join(fs.Args(), " "), Status: status, MinAmount: *minAmount, MaxAmount: *maxAmount, Category: *category, Merchant: *merchant, Limit: *limit}, *jsonOut)
}

func (a App) spend(args []string) error {
	fs := flag.NewFlagSet("spend", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	jsonOut := fs.Bool("json", false, "emit JSON")
	group := fs.String("group", "merchant", "merchant, category, account, or month")
	from := fs.String("from", "", "start date YYYY-MM-DD")
	to := fs.String("to", "", "end date YYYY-MM-DD; defaults to today")
	since := fs.String("since", "month", "relative window")
	if err := fs.Parse(args); err != nil {
		return err
	}
	startDate, endDate, err := dateWindow(*from, *to, *since)
	if err != nil {
		return err
	}
	state, err := loadState(*configPath, false)
	if err != nil {
		return err
	}
	db, err := openStore(state)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Spend(*group, startDate, endDate)
	if err != nil {
		return err
	}
	if a.envelopeJSON() {
		return a.writeRows("spend.summary", rows, len(rows), "cache", nil)
	}
	if *jsonOut {
		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	fmt.Fprintf(a.Out, "spend by %s, %s → %s\n", *group, startDate, endDate)
	for _, r := range rows {
		fmt.Fprintf(a.Out, "%10.2f\t%4d\t%s\t%s\n", r.Spend, r.Count, fallback(r.Currency, ""), r.Group)
	}
	return nil
}

func (a App) merchants(args []string) error {
	if len(args) > 0 && args[0] != "top" {
		return fmt.Errorf("usage: lasso merchants top [--since 90d]")
	}
	fs := flag.NewFlagSet("merchants top", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	jsonOut := fs.Bool("json", false, "emit JSON")
	from := fs.String("from", "", "start date YYYY-MM-DD")
	to := fs.String("to", "", "end date YYYY-MM-DD; defaults to today")
	since := fs.String("since", "90d", "relative window")
	if len(args) > 0 && args[0] == "top" {
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	startDate, endDate, err := dateWindow(*from, *to, *since)
	if err != nil {
		return err
	}
	state, err := loadState(*configPath, false)
	if err != nil {
		return err
	}
	db, err := openStore(state)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Spend("merchant", startDate, endDate)
	if err != nil {
		return err
	}
	if a.envelopeJSON() {
		return a.writeRows("merchant.top", rows, len(rows), "cache", nil)
	}
	if *jsonOut {
		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	fmt.Fprintf(a.Out, "top merchants, %s → %s\n", startDate, endDate)
	for _, r := range rows {
		fmt.Fprintf(a.Out, "%10.2f\t%4d\t%s\t%s\n", r.Spend, r.Count, fallback(r.Currency, ""), r.Group)
	}
	return nil
}

func (a App) cashflow(args []string) error {
	fs := flag.NewFlagSet("cashflow", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	jsonOut := fs.Bool("json", false, "emit JSON")
	from := fs.String("from", "", "start date YYYY-MM-DD")
	to := fs.String("to", "", "end date YYYY-MM-DD; defaults to today")
	since := fs.String("since", "6mo", "relative window: 30d, 90d, 6mo, 1y, ytd")
	if err := fs.Parse(args); err != nil {
		return err
	}
	startDate, endDate, err := dateWindow(*from, *to, *since)
	if err != nil {
		return err
	}
	state, err := loadState(*configPath, false)
	if err != nil {
		return err
	}
	db, err := openStore(state)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Cashflow(startDate, endDate)
	if err != nil {
		return err
	}
	if a.envelopeJSON() {
		return a.writeRows("cashflow.summary", rows, len(rows), "cache", nil)
	}
	if *jsonOut {
		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	fmt.Fprintf(a.Out, "cashflow, %s → %s\n", startDate, endDate)
	for _, r := range rows {
		fmt.Fprintf(a.Out, "%s\tin=%10.2f\tout=%10.2f\tnet=%10.2f\t%4d\t%s\n", r.Month, r.Inflow, r.Outflow, r.Net, r.Count, fallback(r.Currency, ""))
	}
	return nil
}

func (a App) export(args []string) error {
	if len(args) == 0 || args[0] != "tx" {
		return fmt.Errorf("usage: lasso export tx [--format csv|json|jsonl] [--out path]")
	}
	fs := flag.NewFlagSet("export tx", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	format := fs.String("format", "csv", "csv, json, or jsonl")
	outPath := fs.String("out", "", "output path; defaults to stdout")
	from := fs.String("from", "", "start date YYYY-MM-DD")
	to := fs.String("to", "", "end date YYYY-MM-DD; defaults to today")
	since := fs.String("since", "30d", "relative window")
	limit := fs.Int("limit", 10000, "max rows")
	status := fs.String("status", "", "status filter: pending or posted")
	minAmount := fs.String("min", "", "minimum amount")
	maxAmount := fs.String("max", "", "maximum amount")
	category := fs.String("category", "", "category substring")
	merchant := fs.String("merchant", "", "merchant/counterparty substring")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	startDate, endDate, err := dateWindow(*from, *to, *since)
	if err != nil {
		return err
	}
	state, err := loadState(*configPath, false)
	if err != nil {
		return err
	}
	db, err := openStore(state)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := validateStatusValue(*status); err != nil {
		return err
	}
	if err := validateAmountFilter(*minAmount, "--min"); err != nil {
		return err
	}
	if err := validateAmountFilter(*maxAmount, "--max"); err != nil {
		return err
	}
	rows, err := db.QueryTransactions(store.TxFilter{From: startDate, To: endDate, Status: *status, MinAmount: *minAmount, MaxAmount: *maxAmount, Category: *category, Merchant: *merchant, Limit: *limit})
	if err != nil {
		return err
	}
	w := a.Out
	var file *os.File
	if *outPath != "" {
		file, err = os.Create(*outPath)
		if err != nil {
			return err
		}
		defer file.Close()
		w = file
	}
	switch *format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		err = enc.Encode(rows)
	case "jsonl":
		enc := json.NewEncoder(w)
		for _, r := range rows {
			if e := enc.Encode(r); e != nil {
				err = e
				break
			}
		}
	case "csv":
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"date", "amount", "currency", "status", "account", "last_four", "counterparty", "description", "category", "id"})
		for _, r := range rows {
			_ = cw.Write([]string{r.Date, r.Amount, r.Currency, r.Status, r.AccountName, r.AccountLastFour, r.CounterpartyName, r.Description, r.Category, r.ID})
		}
		cw.Flush()
		err = cw.Error()
	default:
		err = fmt.Errorf("unsupported --format %q", *format)
	}
	if err != nil {
		return err
	}
	if *outPath != "" {
		fmt.Fprintf(a.Out, "wrote %d transactions to %s\n", len(rows), *outPath)
	}
	return nil
}

func (a App) cache(args []string) error {
	if len(args) == 0 || args[0] != "status" {
		return fmt.Errorf("usage: lasso cache status")
	}
	fs := flag.NewFlagSet("cache status", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	state, err := loadState(*configPath, false)
	if err != nil {
		return err
	}
	db, err := openStore(state)
	if err != nil {
		return err
	}
	defer db.Close()
	summary, err := db.CacheSummary()
	if err != nil {
		return err
	}
	if a.envelopeJSON() {
		return a.writeEnvelope("cache.status", summary, map[string]any{"cache_path": dbPath(state)}, []nextAction{{Command: "lasso sync run --format json", Description: "Refresh the cache"}})
	}
	fmt.Fprintf(a.Out, "cache: %s\n", dbPath(state))
	for _, key := range []string{"accounts", "balances", "transactions", "sync_runs"} {
		fmt.Fprintf(a.Out, "%s: %d\n", key, summary.Counts[key])
	}
	if summary.LastSyncAt != "" {
		fmt.Fprintf(a.Out, "last_sync: %s %s→%s %s\n", summary.LastSyncAt, summary.LastSyncStart, summary.LastSyncEnd, summary.LastSyncStatus)
	}
	return nil
}

func openStore(state runtimeState) (*store.Store, error) {
	db, err := store.Open(dbPath(state))
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func dbPath(state runtimeState) string {
	if p := state.Env.Get("TELLER_DB_PATH"); p != "" {
		return config.ExpandHome(p)
	}
	return state.Paths.DBFile
}

func cachedAccounts(db *store.Store) ([]teller.Account, error) {
	accounts, err := db.CachedAccounts()
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no cached accounts; run `lasso sync` first")
	}
	return accounts, nil
}

func loadState(configPath string, withClient bool) (runtimeState, error) {
	paths, err := config.ResolvePaths(configPath)
	if err != nil {
		return runtimeState{}, err
	}
	cfg, err := config.LoadEnvFile(paths.ConfigFile)
	if err != nil {
		return runtimeState{}, fmt.Errorf("load config %s: %w", paths.ConfigFile, err)
	}
	enrollmentPath := config.ResolveEnrollmentPath(paths, cfg)
	enrollment, err := teller.LoadEnrollment(enrollmentPath)
	if err != nil {
		return runtimeState{}, fmt.Errorf("load enrollment %s: %w", enrollmentPath, err)
	}
	state := runtimeState{Paths: paths, Env: cfg, EnrollmentPath: enrollmentPath, Enrollment: enrollment}
	if withClient {
		client, err := teller.NewClient(teller.Options{
			BaseURL:  cfg.GetDefault("TELLER_BASE_URL", teller.DefaultBaseURL),
			Env:      cfg.GetDefault("TELLER_ENV", "sandbox"),
			CertPath: config.ExpandHome(cfg.Get("TELLER_CERT_PATH")),
			KeyPath:  config.ExpandHome(cfg.Get("TELLER_KEY_PATH")),
		})
		if err != nil {
			return runtimeState{}, err
		}
		state.Client = client
	}
	return state, nil
}

func checkEnvPresent(w io.Writer, cfg config.Env, key string) bool {
	if cfg.Get(key) == "" {
		fmt.Fprintf(w, "[missing] %s\n", key)
		return false
	}
	fmt.Fprintf(w, "[ok] %s is set\n", key)
	return true
}

func checkPathIfPresent(w io.Writer, value, label string) bool {
	if value == "" {
		return false
	}
	expanded := config.ExpandHome(value)
	if _, err := os.Stat(expanded); err != nil {
		fmt.Fprintf(w, "[missing] %s file: %v\n", label, err)
		return false
	}
	fmt.Fprintf(w, "[ok] %s file exists: %s\n", label, filepath.Clean(expanded))
	checkFileMode(w, expanded)
	return true
}

func checkFileMode(w io.Writer, path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		fmt.Fprintf(w, "[warn] %s permissions are %04o; prefer 0600 for secret-bearing files\n", path, mode)
		return
	}
	fmt.Fprintf(w, "[ok] %s permissions are %04o\n", path, mode)
}

func fallback(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func counterpartyName(details map[string]any) string {
	counterparty, ok := details["counterparty"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := counterparty["name"].(string)
	return name
}

func statusFilter(pending, posted, all bool) (string, error) {
	if all && (pending || posted) {
		return "", fmt.Errorf("--all cannot be combined with --pending or --posted")
	}
	if pending && posted {
		return "", fmt.Errorf("choose only one of --pending or --posted")
	}
	if pending {
		return "pending", nil
	}
	if posted {
		return "posted", nil
	}
	return "", nil
}

func validateStatusValue(status string) error {
	switch status {
	case "", "pending", "posted":
		return nil
	default:
		return fmt.Errorf("unsupported status %q; use pending or posted", status)
	}
}

func validateAmountFilter(value, label string) error {
	if value == "" {
		return nil
	}
	if _, err := strconv.ParseFloat(value, 64); err != nil {
		return fmt.Errorf("invalid %s amount %q", label, value)
	}
	return nil
}

func dateWindow(from, to, since string) (string, string, error) {
	end := time.Now().Format(time.DateOnly)
	if to != "" {
		if _, err := time.Parse(time.DateOnly, to); err != nil {
			return "", "", fmt.Errorf("invalid --to date: %w", err)
		}
		end = to
	}
	if from != "" {
		if _, err := time.Parse(time.DateOnly, from); err != nil {
			return "", "", fmt.Errorf("invalid --from date: %w", err)
		}
		return from, end, nil
	}
	now := time.Now()
	switch since {
	case "month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format(time.DateOnly), end, nil
	case "last-month":
		firstThisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		firstLastMonth := firstThisMonth.AddDate(0, -1, 0)
		lastLastMonth := firstThisMonth.AddDate(0, 0, -1)
		return firstLastMonth.Format(time.DateOnly), lastLastMonth.Format(time.DateOnly), nil
	case "year", "ytd":
		return time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location()).Format(time.DateOnly), end, nil
	case "":
		since = "30d"
	}
	if strings.HasSuffix(since, "mo") {
		months, err := strconv.Atoi(strings.TrimSuffix(since, "mo"))
		if err != nil || months < 0 {
			return "", "", fmt.Errorf("invalid --since %q", since)
		}
		return now.AddDate(0, -months, 0).Format(time.DateOnly), end, nil
	}
	if strings.HasSuffix(since, "y") {
		years, err := strconv.Atoi(strings.TrimSuffix(since, "y"))
		if err != nil || years < 0 {
			return "", "", fmt.Errorf("invalid --since %q", since)
		}
		return now.AddDate(-years, 0, 0).Format(time.DateOnly), end, nil
	}
	if strings.HasSuffix(since, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(since, "d"))
		if err != nil || days < 0 {
			return "", "", fmt.Errorf("invalid --since %q", since)
		}
		return now.AddDate(0, 0, -days).Format(time.DateOnly), end, nil
	}
	return "", "", fmt.Errorf("unsupported --since %q; use 7d, 30d, 90d, month, last-month, 6mo, 1y, year, or ytd", since)
}

func selectAccount(accounts []teller.Account, selector string) (teller.Account, error) {
	if len(accounts) == 0 {
		return teller.Account{}, fmt.Errorf("no accounts found")
	}
	if selector == "" {
		if len(accounts) == 1 {
			return accounts[0], nil
		}
		return teller.Account{}, fmt.Errorf("multiple accounts found; pass --account with id, last four, or name substring")
	}
	selector = strings.ToLower(selector)
	var matches []teller.Account
	for _, account := range accounts {
		if strings.ToLower(account.ID) == selector || strings.ToLower(account.LastFour) == selector || strings.Contains(strings.ToLower(account.Name), selector) {
			matches = append(matches, account)
		}
	}
	if len(matches) == 0 {
		return teller.Account{}, fmt.Errorf("no account matches %q", selector)
	}
	if len(matches) > 1 {
		return teller.Account{}, fmt.Errorf("multiple accounts match %q; use full account id", selector)
	}
	return matches[0], nil
}

func explainTellerError(err error) error {
	var tellerErr teller.Error
	if errors.As(err, &tellerErr) {
		switch tellerErr.StatusCode {
		case 400:
			return fmt.Errorf("%w\ncheck Teller mTLS certificate configuration", err)
		case 401, 403:
			return fmt.Errorf("%w\naccess token is missing, invalid, revoked, or paired with the wrong cert; reconnect", err)
		case 404:
			if strings.HasPrefix(tellerErr.Code, "enrollment.disconnected") {
				return fmt.Errorf("%w\nenrollment needs user action; reconnect or log in to the institution", err)
			}
		case 410:
			return fmt.Errorf("%w\naccount appears closed or no longer accessible", err)
		case 429:
			return fmt.Errorf("%w\nrate limited; retry later", err)
		case 502:
			return fmt.Errorf("%w\ninstitution unavailable; retry later", err)
		}
	}
	return err
}
