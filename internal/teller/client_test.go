package teller

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestGetRetriesRetryableStatuses(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"code":"bad_gateway","message":"institution unavailable"}}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client, err := NewClient(Options{BaseURL: server.URL, Env: "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	client.retryDelay = []time.Duration{0, 0}
	accounts, err := client.ListAccounts(Enrollment{AccessToken: "token_test"})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(accounts) != 0 {
		t.Fatalf("attempts = %d, accounts = %#v", attempts, accounts)
	}
}

func TestGetDoesNotRetryAuthErrors(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"invalid token"}}`))
	}))
	defer server.Close()

	client, err := NewClient(Options{BaseURL: server.URL, Env: "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	client.retryDelay = []time.Duration{0, 0}
	if _, err := client.ListAccounts(Enrollment{AccessToken: "token_test"}); err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}
