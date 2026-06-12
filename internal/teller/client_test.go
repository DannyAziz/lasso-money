package teller

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListAccountsAndBalances(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "token_test" || pass != "" {
			t.Fatalf("unexpected auth user=%q pass=%q ok=%v", user, pass, ok)
		}
		switch r.URL.Path {
		case "/accounts":
			_, _ = w.Write([]byte(`[{"id":"acc_1","enrollment_id":"enr_1","institution":{"id":"amex","name":"American Express"},"name":"Gold Card","type":"credit","subtype":"credit_card","currency":"USD","last_four":"1234","status":"open","links":{"balances":"/accounts/acc_1/balances"}}]`))
		case "/accounts/acc_1/balances":
			_, _ = w.Write([]byte(`{"account_id":"acc_1","ledger":"123.45","available":"100.00"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(Options{BaseURL: server.URL, Env: "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	enrollment := Enrollment{ID: "enr_1", AccessToken: "token_test"}
	accounts, err := client.ListAccounts(enrollment)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Name != "Gold Card" || accounts[0].LastFour != "1234" {
		t.Fatalf("accounts = %#v", accounts)
	}
	balance, err := client.GetBalance(enrollment, "acc_1")
	if err != nil {
		t.Fatal(err)
	}
	if balance.Ledger != "123.45" || balance.Available != "100.00" {
		t.Fatalf("balance = %#v", balance)
	}
}
