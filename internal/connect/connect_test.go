package connect

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallbackRequiresTokenAndJSONContentType(t *testing.T) {
	result := make(chan connectResult, 1)
	h := handler(Options{ApplicationID: "app_test", Environment: "sandbox"}, "tok_secret", result)
	server := httptest.NewServer(h)
	defer server.Close()

	body := `{"accessToken":"token_attacker"}`

	// Missing token: rejected.
	resp, err := http.Post(server.URL+"/callback", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no token: status = %d, want 403", resp.StatusCode)
	}

	// Valid token but non-JSON content type (cross-origin form trick): rejected.
	resp, err = http.Post(server.URL+"/callback?token=tok_secret", "text/plain", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain: status = %d, want 415", resp.StatusCode)
	}

	// Cancel without the token: rejected.
	resp, err = http.Post(server.URL+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cancel without token: status = %d, want 403", resp.StatusCode)
	}
	select {
	case res := <-result:
		t.Fatalf("unauthorized request delivered a result: %#v", res)
	default:
	}

	// Valid token + JSON: accepted.
	resp, err = http.Post(server.URL+"/callback?token=tok_secret", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized: status = %d, want 200", resp.StatusCode)
	}
	res := <-result
	if res.err != nil || res.enrollment.AccessToken != "token_attacker" {
		t.Fatalf("result = %#v", res)
	}
}

func TestPageEmbedsToken(t *testing.T) {
	result := make(chan connectResult, 1)
	h := handler(Options{ApplicationID: "app_test", Environment: "sandbox"}, "tok_secret", result)
	server := httptest.NewServer(h)
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 1<<16)
	n, _ := resp.Body.Read(buf)
	page := string(buf[:n])
	if !strings.Contains(page, "/callback?token=tok_secret") {
		t.Fatal("page does not embed the callback token")
	}
	if strings.Contains(page, "__TOKEN__") {
		t.Fatal("page still contains the token placeholder")
	}
}
