package store

import (
	"path/filepath"
	"testing"

	"github.com/dannyaziz/lasso-money/internal/teller"
)

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

	status, err := s.CacheStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status["accounts"] != 1 || status["balances"] != 1 || status["transactions"] != 2 {
		t.Fatalf("status = %#v", status)
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

	spend, err := s.Spend("category", "2026-06-01", "2026-06-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(spend) != 2 || spend[0].Spend < spend[1].Spend {
		t.Fatalf("spend = %#v", spend)
	}
}
