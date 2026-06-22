package cli

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dannyaziz/lasso-money/internal/store"
	"github.com/dannyaziz/lasso-money/internal/teller"
)

// clearEnvOverrides blanks process env vars that config.Env lets override
// config-file values, so a developer's live setup cannot leak into tests.
func clearEnvOverrides(t *testing.T) {
	t.Helper()
	for _, key := range []string{"TELLER_APPLICATION_ID", "TELLER_ENV", "TELLER_CERT_PATH", "TELLER_KEY_PATH", "TELLER_ENROLLMENT_PATH", "TELLER_DB_PATH", "TELLER_BASE_URL", "LASSO_FORMAT"} {
		t.Setenv(key, "")
	}
}

// testSetup writes a sandbox config/enrollment and returns the config path
// and the cache database path.
func testSetup(t *testing.T) (string, string) {
	t.Helper()
	clearEnvOverrides(t)
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

func TestAccountsListsMultipleEnrollments(t *testing.T) {
	configPath, _ := testSetup(t)
	cfg, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	enrollmentPath := strings.TrimSpace(strings.Split(strings.Split(string(cfg), "TELLER_ENROLLMENT_PATH=")[1], "\n")[0])
	if err := teller.AddEnrollment(enrollmentPath, teller.Enrollment{ID: "enr_2", AccessToken: "token_2"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _, _ := r.BasicAuth()
		switch token {
		case "token_test":
			_, _ = w.Write([]byte(`[{"id":"acc_1","enrollment_id":"enr_1","name":"Checking"}]`))
		case "token_2":
			_, _ = w.Write([]byte(`[{"id":"acc_2","enrollment_id":"enr_2","name":"Savings"}]`))
		default:
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}
	}))
	defer server.Close()
	file, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(file, "TELLER_BASE_URL=%s\n", server.URL)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, code := runCLI(t, "accounts", "--format", "json", "--config", configPath)
	if code != 0 || !strings.Contains(out, "acc_1") || !strings.Contains(out, "acc_2") {
		t.Fatalf("exit=%d stderr=%q out=%s", code, errOut, out)
	}
}

func TestInvalidEnrollmentFileCannotPruneCache(t *testing.T) {
	configPath, dbPath := testSetup(t)
	seedCache(t, dbPath)
	cfg, _ := os.ReadFile(configPath)
	enrollmentPath := strings.TrimSpace(strings.Split(strings.Split(string(cfg), "TELLER_ENROLLMENT_PATH=")[1], "\n")[0])
	if err := os.WriteFile(enrollmentPath, []byte(`{"access_token":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runCLI(t, "balances", "--live", "--config", configPath); code == 0 {
		t.Fatal("invalid enrollment file should fail")
	}
	cache, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	accounts, err := cache.CachedAccounts()
	if err != nil || len(accounts) != 1 || accounts[0].Status == "__lasso_removed" {
		t.Fatalf("accounts = %#v, err = %v", accounts, err)
	}
}

func TestBalancesRoutesMultipleEnrollmentsAndPrunesUnion(t *testing.T) {
	configPath, dbPath := testSetup(t)
	cfg, _ := os.ReadFile(configPath)
	enrollmentPath := strings.TrimSpace(strings.Split(strings.Split(string(cfg), "TELLER_ENROLLMENT_PATH=")[1], "\n")[0])
	if err := teller.AddEnrollment(enrollmentPath, teller.Enrollment{ID: "enr_2", AccessToken: "token_2"}); err != nil {
		t.Fatal(err)
	}
	cache, _ := store.Open(dbPath)
	_ = cache.Migrate()
	stale := teller.Account{ID: "acc_stale", EnrollmentID: "enr_old", Name: "Stale"}
	_ = cache.UpsertAccounts([]teller.Account{stale})
	_ = cache.UpsertBalance(stale, teller.Balance{Ledger: "999"})
	_ = cache.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _, _ := r.BasicAuth()
		if r.URL.Path == "/accounts" {
			if token == "token_test" {
				_, _ = w.Write([]byte(`[{"id":"acc_1","enrollment_id":"enr_1","name":"Checking"}]`))
			} else {
				_, _ = w.Write([]byte(`[{"id":"acc_2","enrollment_id":"enr_2","name":"Savings"}]`))
			}
			return
		}
		if (r.URL.Path == "/accounts/acc_1/balances" && token == "token_test") || (r.URL.Path == "/accounts/acc_2/balances" && token == "token_2") {
			_, _ = w.Write([]byte(`{"ledger":"10.00"}`))
			return
		}
		http.Error(w, "wrong token", http.StatusUnauthorized)
	}))
	defer server.Close()
	file, _ := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = fmt.Fprintf(file, "TELLER_BASE_URL=%s\n", server.URL)
	_ = file.Close()
	out, errOut, code := runCLI(t, "balances", "--live", "--format", "json", "--config", configPath)
	if code != 0 || !strings.Contains(out, "acc_1") || !strings.Contains(out, "acc_2") || strings.Contains(out, "acc_stale") {
		t.Fatalf("exit=%d stderr=%q out=%s", code, errOut, out)
	}
}

func TestWhoamiPreservesSingleShapeAndUsesArrayForMultiple(t *testing.T) {
	configPath, _ := testSetup(t)
	out, errOut, code := runCLI(t, "whoami", "--format", "json", "--config", configPath)
	if code != 0 || strings.Contains(out, `"data": [`) {
		t.Fatalf("single: exit=%d stderr=%q out=%s", code, errOut, out)
	}
	cfg, _ := os.ReadFile(configPath)
	enrollmentPath := strings.TrimSpace(strings.Split(strings.Split(string(cfg), "TELLER_ENROLLMENT_PATH=")[1], "\n")[0])
	if err := teller.AddEnrollment(enrollmentPath, teller.Enrollment{ID: "enr_2", AccessToken: "token_2"}); err != nil {
		t.Fatal(err)
	}
	out, errOut, code = runCLI(t, "whoami", "--format", "json", "--config", configPath)
	if code != 0 || !strings.Contains(out, `"data": [`) || strings.Contains(out, "token_test") || strings.Contains(out, "token_2\"") {
		t.Fatalf("multiple: exit=%d stderr=%q out=%s", code, errOut, out)
	}
}

func TestBalanceCacheFresh(t *testing.T) {
	now := time.Now()
	fresh := []store.BalanceRow{{AsOf: now.Add(-4 * time.Minute).Format(time.RFC3339Nano)}}
	expired := []store.BalanceRow{{AsOf: now.Add(-5 * time.Minute).Format(time.RFC3339Nano)}}
	if !balanceCacheFresh(fresh, now) {
		t.Fatal("four-minute-old balance should be fresh")
	}
	if balanceCacheFresh(expired, now) || balanceCacheFresh(nil, now) {
		t.Fatal("expired or empty balance cache should refresh")
	}
}

func TestBalancesLiveRefreshesThenUsesCache(t *testing.T) {
	configPath, dbPath := testSetup(t)
	cache, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Migrate(); err != nil {
		t.Fatal(err)
	}
	obsolete := teller.Account{ID: "acc_obsolete", Name: "Closed account"}
	if err := cache.UpsertAccounts([]teller.Account{obsolete}); err != nil {
		t.Fatal(err)
	}
	if err := cache.UpsertBalance(obsolete, teller.Balance{Ledger: "999.00"}); err != nil {
		t.Fatal(err)
	}
	_ = cache.Close()

	balanceCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accounts":
			_, _ = w.Write([]byte(`[{"id":"acc_1","name":"Checking","type":"depository","currency":"USD","last_four":"4321","status":"open"}]`))
		case "/accounts/acc_1/balances":
			balanceCalls++
			_, _ = w.Write([]byte(`{"account_id":"acc_1","ledger":"125.00","available":"100.00"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	file, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(file, "TELLER_BASE_URL=%s\n", server.URL)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}

	out, errOut, code := runCLI(t, "balances", "--live", "--format", "json", "--config", configPath)
	if code != 0 || balanceCalls != 1 || !strings.Contains(out, `"as_of"`) || !strings.Contains(out, `"source": "live"`) || strings.Contains(out, "acc_obsolete") {
		t.Fatalf("live: exit=%d calls=%d stderr=%q out=%s", code, balanceCalls, errOut, out)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE balances SET as_of=?`, time.Now().Add(-balanceCacheTTL).Format(time.RFC3339Nano))
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, code = runCLI(t, "balances", "--format", "json", "--config", configPath)
	if code != 0 || balanceCalls != 2 || !strings.Contains(out, `"source": "live"`) {
		t.Fatalf("expired: exit=%d calls=%d stderr=%q out=%s", code, balanceCalls, errOut, out)
	}
	server.Close()
	out, errOut, code = runCLI(t, "balances", "--format", "json", "--config", configPath)
	if code != 0 || balanceCalls != 2 || !strings.Contains(out, `"source": "cache"`) || !strings.Contains(out, `"as_of"`) || strings.Contains(out, "acc_obsolete") {
		t.Fatalf("cache: exit=%d calls=%d stderr=%q out=%s", code, balanceCalls, errOut, out)
	}
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

func TestSearchEnvelopeReportsSearchCommand(t *testing.T) {
	configPath, dbPath := testSetup(t)
	seedCache(t, dbPath)

	out, errOut, code := runCLI(t, "--format", "json", "search", "amazon", "--config", configPath)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	var env struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if env.Command != "transaction.search" {
		t.Fatalf("command = %q, want transaction.search", env.Command)
	}
}

func TestExportKeepsItsFormatFlag(t *testing.T) {
	configPath, dbPath := testSetup(t)
	seedCache(t, dbPath)

	// Plain export: --format selects the file format.
	out, errOut, code := runCLI(t, "export", "tx", "--format", "csv", "--since", "30d", "--config", configPath)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	if !strings.HasPrefix(out, "date,amount,") {
		t.Fatalf("expected CSV header, got %q", out)
	}

	// A global --format before the command must not consume export's own
	// --format; --format means two different things here.
	out, errOut, code = runCLI(t, "--format", "json", "export", "tx", "--format", "csv", "--since", "30d", "--config", configPath)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	if !strings.HasPrefix(out, "date,amount,") {
		t.Fatalf("expected CSV header with leading global --format, got %q", out)
	}
}

func TestTellerUpstreamErrorsMapToContract(t *testing.T) {
	for _, status := range []int{502, 504} {
		err := explainTellerError(teller.Error{StatusCode: status, Path: "/accounts"})
		var coded codedError
		if !errors.As(err, &coded) {
			t.Fatalf("status %d: not a codedError: %#v", status, err)
		}
		if coded.code != "upstream_unavailable" || coded.exitCode != 6 || !coded.retryable {
			t.Fatalf("status %d: coded = %#v", status, coded)
		}
	}
}

func TestMerchantTopAcceptsLimit(t *testing.T) {
	configPath, dbPath := testSetup(t)
	seedCache(t, dbPath)
	if _, _, code := runCLI(t, "merchants"); code != 2 {
		t.Fatalf("merchants without top exit = %d, want 2", code)
	}

	out, errOut, code := runCLI(t, "--format", "json", "merchants", "top", "--since", "90d", "--limit", "5", "--config", configPath)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	var env struct {
		Command string            `json:"command"`
		Data    []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if env.Command != "merchant.top" || len(env.Data) != 1 {
		t.Fatalf("envelope = %s", out)
	}
}

func TestErrorsEmitEnvelopeAndExitCodes(t *testing.T) {
	clearEnvOverrides(t)
	missing := filepath.Join(t.TempDir(), "missing.env")

	out, _, code := runCLI(t, "--format", "json", "tx", "--config", missing)
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
	if _, _, code := runCLI(t, "transaction", "list"); code != 2 {
		t.Fatalf("removed command exit = %d, want 2", code)
	}
	if _, _, code := runCLI(t, "accounts", "--json"); code != 2 {
		t.Fatalf("removed --json exit = %d, want 2", code)
	}
}

func TestDoctorJSONNotReady(t *testing.T) {
	clearEnvOverrides(t)
	missing := filepath.Join(t.TempDir(), "missing.env")

	out, _, code := runCLI(t, "--format", "json", "doctor", "--config", missing)
	if code != 4 {
		t.Fatalf("exit = %d, want 4", code)
	}
	var env struct {
		OK    bool             `json:"ok"`
		Data  []doctorCheck    `json:"data"`
		Meta  map[string]any   `json:"meta"`
		Error *structuredError `json:"error"`
	}
	dec := json.NewDecoder(strings.NewReader(out))
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	// Doctor reports its own envelope; Main must not append a second one.
	if dec.More() {
		t.Fatalf("expected exactly one JSON document on stdout:\n%s", out)
	}
	if env.OK || env.Error == nil || env.Error.Code != "not_ready" {
		t.Fatalf("envelope = %s", out)
	}
	if ready, _ := env.Meta["ready"].(bool); ready {
		t.Fatalf("meta.ready = true, want false")
	}
	if len(env.Data) == 0 || env.Data[0].Name != "config_file" || env.Data[0].Status != "missing" {
		t.Fatalf("checks = %#v", env.Data)
	}
}

func TestDoctorJSONReady(t *testing.T) {
	configPath, _ := testSetup(t)

	out, errOut, code := runCLI(t, "--format", "json", "doctor", "--config", configPath)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, errOut, out)
	}
	var env struct {
		OK   bool           `json:"ok"`
		Data []doctorCheck  `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if !env.OK {
		t.Fatalf("envelope = %s", out)
	}
	if ready, _ := env.Meta["ready"].(bool); !ready {
		t.Fatalf("meta.ready = false:\n%s", out)
	}
	for _, c := range env.Data {
		if c.Status == "missing" {
			t.Fatalf("unexpected missing check %q", c.Name)
		}
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
	t.Setenv("LASSO_FORMAT", "")
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
}

func TestSchemaArgsFromCommand(t *testing.T) {
	cases := map[string]string{
		"accounts":      "account.list",
		"balances":      "balance.list",
		"tx":            "transaction.list",
		"search":        "transaction.search",
		"whoami":        "whoami",
		"merchants top": "merchant.top",
		"export tx":     "transaction.export",
		"cache status":  "cache.status",
	}
	for in, want := range cases {
		got := schemaArgsFromCommand(strings.Fields(in))
		if len(got) != 1 || got[0] != want {
			t.Fatalf("schemaArgsFromCommand(%q) = %v, want %q", in, got, want)
		}
	}
	for _, args := range [][]string{{"schema", "balances", "extra"}, {"balances", "--schema", "extra"}} {
		if _, _, code := runCLI(t, args...); code != 2 {
			t.Fatalf("schema with extra argument %v exit = %d, want 2", args, code)
		}
	}
	valid := []struct {
		args []string
		name string
	}{
		{[]string{"schema", "balances"}, "balance.list"},
		{[]string{"balances", "--schema"}, "balance.list"},
		{[]string{"merchants", "top", "--schema"}, "merchant.top"},
		{[]string{"export", "tx", "--schema"}, "transaction.export"},
		{[]string{"cache", "status", "--schema"}, "cache.status"},
	}
	for _, tc := range valid {
		out, errOut, code := runCLI(t, tc.args...)
		if code != 0 || !strings.Contains(out, `"name": "`+tc.name+`"`) {
			t.Fatalf("schema invocation %v: exit=%d stderr=%q out=%s", tc.args, code, errOut, out)
		}
	}
}

func TestCommandSchemaFlagsMatchHandlers(t *testing.T) {
	want := map[string][]string{
		"transaction.list":   {"--config", "--account", "--from", "--to", "--since", "--limit", "--pending", "--posted", "--min", "--max", "--category", "--merchant", "--live"},
		"transaction.search": {"query", "--config", "--from", "--to", "--since", "--limit", "--pending", "--posted", "--min", "--max", "--category", "--merchant"},
		"transaction.export": {"--config", "--format csv|json|jsonl", "--out", "--from", "--to", "--since", "--limit", "--status", "--min", "--max", "--category", "--merchant"},
		"spend.summary":      {"--config", "--group merchant|category|account|month", "--from", "--to", "--since", "--limit"},
		"merchant.top":       {"--config", "--from", "--to", "--since", "--limit"},
		"cashflow.summary":   {"--config", "--from", "--to", "--since"},
	}
	for name, flags := range want {
		entry := commandSchemas()[name].(map[string]any)
		if got := fmt.Sprint(entry["flags"]); got != fmt.Sprint(flags) {
			t.Errorf("%s flags = %s, want %v", name, got, flags)
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
	if _, err := statusFilter(true, true); err == nil {
		t.Fatal("want error for --pending --posted")
	}
	if got, err := statusFilter(true, false); err != nil || got != "pending" {
		t.Fatalf("got = %q, err = %v", got, err)
	}
	if got, err := statusFilter(false, false); err != nil || got != "" {
		t.Fatalf("got = %q, err = %v", got, err)
	}
}
