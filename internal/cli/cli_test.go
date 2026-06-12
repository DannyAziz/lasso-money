package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dannyaziz/lasso-money/internal/store"
	"github.com/dannyaziz/lasso-money/internal/teller"
)

// testSetup writes a sandbox config/enrollment and returns the config path
// and the cache database path.
func testSetup(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.env")
	enrollmentPath := filepath.Join(dir, "enrollment.json")
	dbPath := filepath.Join(dir, "lasso.db")
	cfg := fmt.Sprintf("TELLER_APPLICATION_ID=app_test\nTELLER_ENV=sandbox\nTELLER_ENROLLMENT_PATH=%s\nTELLER_DB_PATH=%s\n", enrollmentPath, dbPath)
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := teller.SaveEnrollment(enrollmentPath, teller.Enrollment{ID: "enr_1", AccessToken: "token_test"}); err != nil {
		t.Fatal(err)
	}
	return configPath, dbPath
}

func seedCache(t *testing.T, dbPath string) {
	t.Helper()
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	account := teller.Account{ID: "acc_1", EnrollmentID: "enr_1", Name: "Gold Card", Type: "credit", Currency: "USD", LastFour: "1234"}
	if err := db.UpsertAccounts([]teller.Account{account}); err != nil {
		t.Fatal(err)
	}
	date := time.Now().AddDate(0, 0, -5).Format(time.DateOnly)
	if err := db.UpsertTransactions(account, []teller.Transaction{
		{ID: "tx_1", Amount: "25.00", Date: date, Description: "AMAZON.COM PURCHASE", Status: "posted", Details: map[string]any{"counterparty": map[string]any{"name": "Amazon"}}},
	}); err != nil {
		t.Fatal(err)
	}
}

func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Main(args, &out, &errOut)
	return out.String(), errOut.String(), code
}

func TestSearchAcceptsFlagsAfterQuery(t *testing.T) {
	configPath, dbPath := testSetup(t)
	seedCache(t, dbPath)

	// Flags after the positional query must still be parsed; the stdlib
	// flag package used to stop at "amazon" and ignore everything after.
	out, errOut, code := runCLI(t, "search", "amazon", "--since", "90d", "--config", configPath)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, "Amazon") || !strings.Contains(out, "1 cached transactions") {
		t.Fatalf("out = %q", out)
	}

	// Multi-word queries still join.
	out, _, code = runCLI(t, "search", "amazon", "purchase", "--config", configPath)
	if code != 0 || !strings.Contains(out, "0 cached transactions") {
		t.Fatalf("multi-word: exit = %d, out = %q", code, out)
	}
}

func TestEmptyResultsEnvelopeHasEmptyDataArray(t *testing.T) {
	configPath, dbPath := testSetup(t)
	seedCache(t, dbPath)

	out, errOut, code := runCLI(t, "--format", "json", "search", "zzznomatch", "--config", configPath)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	var env struct {
		OK   bool              `json:"ok"`
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if !env.OK || env.Data == nil || len(env.Data) != 0 {
		t.Fatalf("envelope = %s", out)
	}
	if !strings.Contains(out, `"data": []`) {
		t.Fatalf("data must serialize as []:\n%s", out)
	}
}

func TestErrorsEmitEnvelopeAndExitCodes(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.env")

	out, _, code := runCLI(t, "--format", "json", "transaction", "list", "--config", missing)
	if code != 4 {
		t.Fatalf("exit = %d, want 4", code)
	}
	var env struct {
		OK      bool             `json:"ok"`
		Command string           `json:"command"`
		Error   *structuredError `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if env.OK || env.Command != "transaction.list" || env.Error == nil || env.Error.Code != "config_error" || env.Error.Fix == "" {
		t.Fatalf("envelope = %s", out)
	}

	if _, _, code := runCLI(t, "bogus-command"); code != 2 {
		t.Fatalf("unknown command exit = %d, want 2", code)
	}
	if _, _, code := runCLI(t, "--format", "yaml", "accounts"); code != 2 {
		t.Fatalf("bad format exit = %d, want 2", code)
	}
	if _, _, code := runCLI(t, "tx", "--since", "bananas", "--config", missing); code != 2 {
		t.Fatalf("bad since exit = %d, want 2", code)
	}
}

func TestDateWindow(t *testing.T) {
	today := time.Now().Format(time.DateOnly)
	cases := []struct {
		from, to, since string
		wantFrom        string
		wantTo          string
		wantErr         bool
	}{
		{from: "2026-01-02", to: "2026-02-03", wantFrom: "2026-01-02", wantTo: "2026-02-03"},
		{since: "30d", wantFrom: time.Now().AddDate(0, 0, -30).Format(time.DateOnly), wantTo: today},
		{since: "6mo", wantFrom: time.Now().AddDate(0, -6, 0).Format(time.DateOnly), wantTo: today},
		{since: "1y", wantFrom: time.Now().AddDate(-1, 0, 0).Format(time.DateOnly), wantTo: today},
		{since: "ytd", wantFrom: fmt.Sprintf("%d-01-01", time.Now().Year()), wantTo: today},
		{since: "", wantFrom: time.Now().AddDate(0, 0, -30).Format(time.DateOnly), wantTo: today},
		{since: "bananas", wantErr: true},
		{since: "-5d", wantErr: true},
		{from: "02/01/2026", wantErr: true},
	}
	for _, c := range cases {
		gotFrom, gotTo, err := dateWindow(c.from, c.to, c.since)
		if c.wantErr {
			if err == nil {
				t.Fatalf("dateWindow(%q,%q,%q): want error", c.from, c.to, c.since)
			}
			continue
		}
		if err != nil || gotFrom != c.wantFrom || gotTo != c.wantTo {
			t.Fatalf("dateWindow(%q,%q,%q) = %q,%q,%v; want %q,%q", c.from, c.to, c.since, gotFrom, gotTo, err, c.wantFrom, c.wantTo)
		}
	}
}

func TestParseGlobalArgs(t *testing.T) {
	args, format := parseGlobalArgs([]string{"accounts", "--format", "json"}, "")
	if format != "json" || len(args) != 1 || args[0] != "accounts" {
		t.Fatalf("args = %v, format = %q", args, format)
	}
	args, format = parseGlobalArgs([]string{"tx", "--format=text"}, "")
	if format != "text" || len(args) != 1 {
		t.Fatalf("args = %v, format = %q", args, format)
	}
	// export keeps --format for the file format.
	args, format = parseGlobalArgs([]string{"export", "tx", "--format", "csv"}, "")
	if format != "" || len(args) != 4 {
		t.Fatalf("export args = %v, format = %q", args, format)
	}
	args, format = parseGlobalArgs([]string{"transaction", "export", "--format", "jsonl"}, "")
	if format != "" || len(args) != 4 {
		t.Fatalf("transaction export args = %v, format = %q", args, format)
	}
}

func TestSchemaArgsFromCommand(t *testing.T) {
	cases := map[string]string{
		"accounts":           "account.list",
		"tx":                 "transaction.list",
		"search":             "transaction.search",
		"whoami":             "whoami",
		"account list":       "account.list",
		"transaction search": "transaction.search",
		"cache status":       "cache.status",
	}
	for in, want := range cases {
		got := schemaArgsFromCommand(strings.Fields(in))
		if len(got) != 1 || got[0] != want {
			t.Fatalf("schemaArgsFromCommand(%q) = %v, want %q", in, got, want)
		}
	}
}

func TestSelectAccount(t *testing.T) {
	accounts := []teller.Account{
		{ID: "acc_1", Name: "Gold Card", LastFour: "1234"},
		{ID: "acc_2", Name: "Platinum Card", LastFour: "5678"},
	}
	if _, err := selectAccount(nil, ""); err == nil {
		t.Fatal("want error for no accounts")
	}
	if _, err := selectAccount(accounts, ""); err == nil {
		t.Fatal("want error for ambiguous empty selector")
	}
	got, err := selectAccount(accounts, "5678")
	if err != nil || got.ID != "acc_2" {
		t.Fatalf("got = %#v, err = %v", got, err)
	}
	got, err = selectAccount(accounts, "gold")
	if err != nil || got.ID != "acc_1" {
		t.Fatalf("got = %#v, err = %v", got, err)
	}
	if _, err := selectAccount(accounts, "card"); err == nil {
		t.Fatal("want error for ambiguous substring")
	}
	if _, err := selectAccount(accounts, "nope"); err == nil {
		t.Fatal("want error for no match")
	}
}

func TestStatusFilter(t *testing.T) {
	if _, err := statusFilter(true, true, false); err == nil {
		t.Fatal("want error for --pending --posted")
	}
	if _, err := statusFilter(true, false, true); err == nil {
		t.Fatal("want error for --all --pending")
	}
	if got, err := statusFilter(true, false, false); err != nil || got != "pending" {
		t.Fatalf("got = %q, err = %v", got, err)
	}
	if got, err := statusFilter(false, false, false); err != nil || got != "" {
		t.Fatalf("got = %q, err = %v", got, err)
	}
}
