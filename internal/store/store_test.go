package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/dannyaziz/lasso-money/internal/teller"
)

func TestCachedBalancesIncludesAccountsWithoutBalances(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAccounts([]teller.Account{{ID: "acc_1", Name: "Checking"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.CachedBalances(nil)
	if err != nil || len(rows) != 1 || rows[0].AccountID != "acc_1" || rows[0].AsOf != "" {
		t.Fatalf("balances = %#v, err = %v", rows, err)
	}
}

func TestStoreSyncQuerySpend(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}

	account := teller.Account{ID: "acc_1", EnrollmentID: "enr_1", InstitutionName: "American Express", Name: "Gold Card", Type: "credit", Subtype: "credit_card", Currency: "USD", LastFour: "1234", Status: "open"}
	if err := s.UpsertAccounts([]teller.Account{account}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBalance(account, teller.Balance{AccountID: "acc_1", Ledger: "100.00"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTransactions(account, []teller.Transaction{
		{ID: "tx_1", AccountID: "acc_1", Amount: "12.34", Date: "2026-06-12", Description: "Coffee", Status: "posted", Details: map[string]any{"category": "food", "counterparty": map[string]any{"name": "Cafe"}}},
		{ID: "tx_2", AccountID: "acc_1", Amount: "5.00", Date: "2026-06-11", Description: "Book", Status: "posted", Details: map[string]any{"category": "shopping"}},
	}); err != nil {
		t.Fatal(err)
	}

	summary, err := s.CacheSummary()
	if err != nil {
		t.Fatal(err)
	}
	status := summary.Counts
	if status["accounts"] != 1 || status["balances"] != 1 || status["transactions"] != 2 {
		t.Fatalf("status = %#v", status)
	}
	balances, err := s.CachedBalances(nil)
	if err != nil || len(balances) != 1 || balances[0].Ledger != "100.00" || balances[0].AsOf == "" {
		t.Fatalf("balances = %#v, err = %v", balances, err)
	}

	rows, err := s.QueryTransactions(TxFilter{Query: "caf", From: "2026-06-01", To: "2026-06-30", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CounterpartyName != "Cafe" || rows[0].AccountLastFour != "1234" {
		t.Fatalf("rows = %#v", rows)
	}

	filtered, err := s.QueryTransactions(TxFilter{Status: "posted", MinAmount: "10", Category: "foo", Merchant: "caf", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].ID != "tx_1" {
		t.Fatalf("filtered = %#v", filtered)
	}

	cashflow, err := s.Cashflow("2026-06-01", "2026-06-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(cashflow) != 1 || cashflow[0].Outflow != 17.34 || cashflow[0].Net != -17.34 {
		t.Fatalf("cashflow = %#v", cashflow)
	}

	spend, err := s.Spend("category", "2026-06-01", "2026-06-30", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(spend) != 2 || spend[0].Spend < spend[1].Spend {
		t.Fatalf("spend = %#v", spend)
	}
}

func TestSpendNormalizesDepositorySign(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}

	credit := teller.Account{ID: "acc_credit", Name: "Gold Card", Type: "credit", Currency: "USD"}
	checking := teller.Account{ID: "acc_checking", Name: "Checking", Type: "depository", Currency: "USD"}
	if err := s.UpsertAccounts([]teller.Account{credit, checking}); err != nil {
		t.Fatal(err)
	}
	// Credit charges arrive positive; depository debits arrive negative.
	if err := s.UpsertTransactions(credit, []teller.Transaction{
		{ID: "tx_c1", Amount: "20.00", Date: "2026-06-10", Description: "Coffee", Status: "posted"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTransactions(checking, []teller.Transaction{
		{ID: "tx_d1", Amount: "-30.00", Date: "2026-06-10", Description: "Groceries", Status: "posted"},
		{ID: "tx_d2", Amount: "500.00", Date: "2026-06-10", Description: "Payroll", Status: "posted"},
	}); err != nil {
		t.Fatal(err)
	}

	spend, err := s.Spend("account", "2026-06-01", "2026-06-30", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, r := range spend {
		got[r.Group] = r.Spend
	}
	if got["Gold Card"] != 20 || got["Checking"] != 30 {
		t.Fatalf("spend = %#v", spend)
	}

	cashflow, err := s.Cashflow("2026-06-01", "2026-06-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(cashflow) != 1 {
		t.Fatalf("cashflow = %#v", cashflow)
	}
	if cashflow[0].Outflow != 50 || cashflow[0].Inflow != 500 || cashflow[0].Net != 450 {
		t.Fatalf("cashflow = %#v", cashflow[0])
	}
}

func TestIncrementalStartDateAdvances(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}

	// No prior runs: fall back N days.
	start, err := s.IncrementalStartDate("acc_1", 10, 90)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Now().AddDate(0, 0, -90).Format(time.DateOnly); start != want {
		t.Fatalf("fallback start = %q, want %q", start, want)
	}

	// After a successful run, the window must anchor on its end date, not
	// its start date, so repeated incremental syncs advance.
	end := time.Now().Format(time.DateOnly)
	runID, err := s.StartSyncRun("acc_1", start, end)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishSyncRun(runID, "ok", 1); err != nil {
		t.Fatal(err)
	}
	next, err := s.IncrementalStartDate("acc_1", 10, 90)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Now().AddDate(0, 0, -10).Format(time.DateOnly); next != want {
		t.Fatalf("incremental start = %q, want %q", next, want)
	}
}

func TestLegacySchemaColumnsRemainCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE accounts ADD COLUMN alias TEXT`,
		`ALTER TABLE accounts ADD COLUMN raw_json TEXT`,
		`ALTER TABLE balances ADD COLUMN raw_json TEXT`,
		`ALTER TABLE transactions ADD COLUMN raw_json TEXT`,
		`ALTER TABLE sync_runs ADD COLUMN error_message TEXT`,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	account := teller.Account{ID: "acc_legacy", Name: "Legacy"}
	if err := s.UpsertAccounts([]teller.Account{account}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBalance(account, teller.Balance{Ledger: "1.00"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTransactions(account, []teller.Transaction{{ID: "tx_legacy", Amount: "1.00", Date: "2026-06-18"}}); err != nil {
		t.Fatal(err)
	}
}

func TestQueryFunctionsReturnEmptySlices(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	rows, err := s.QueryTransactions(TxFilter{Limit: 10})
	if err != nil || rows == nil {
		t.Fatalf("rows = %#v, err = %v; want non-nil empty slice", rows, err)
	}
	spend, err := s.Spend("merchant", "", "", 0)
	if err != nil || spend == nil {
		t.Fatalf("spend = %#v, err = %v; want non-nil empty slice", spend, err)
	}
	cashflow, err := s.Cashflow("", "")
	if err != nil || cashflow == nil {
		t.Fatalf("cashflow = %#v, err = %v; want non-nil empty slice", cashflow, err)
	}
}
