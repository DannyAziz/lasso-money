package connect

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dannyaziz/lasso-money/internal/teller"
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

// TestRunEndToEnd drives the full Connect flow the way a browser would:
// receive the URL via OnURL, fetch the page, extract the one-time token,
// and POST the enrollment back.
func TestRunEndToEnd(t *testing.T) {
	enrollmentPath := filepath.Join(t.TempDir(), "enrollment.json")
	urlCh := make(chan string, 1)

	type result struct {
		enrollment teller.Enrollment
		err        error
	}
	done := make(chan result, 1)
	go func() {
		enrollment, err := Run(context.Background(), Options{
			ApplicationID:  "app_test",
			Environment:    "sandbox",
			Timeout:        10 * time.Second,
			OpenBrowser:    false,
			EnrollmentPath: enrollmentPath,
			OnURL:          func(u string) { urlCh <- u },
		})
		done <- result{enrollment, err}
	}()

	var base string
	select {
	case base = <-urlCh:
	case <-time.After(5 * time.Second):
		t.Fatal("OnURL was never called")
	}

	resp, err := http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`/callback\?token=([0-9a-f]+)`).FindSubmatch(body)
	if m == nil {
		t.Fatalf("page does not contain a callback token:\n%s", body)
	}

	payload := `{"accessToken":"token_e2e_secret","enrollment":{"id":"enr_e2e","institution":{"id":"tb","name":"Test Bank"}}}`
	resp, err = http.Post(base+"callback?token="+string(m[1]), "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d", resp.StatusCode)
	}

	res := <-done
	if res.err != nil {
		t.Fatal(res.err)
	}
	if res.enrollment.ID != "enr_e2e" || res.enrollment.InstitutionName != "Test Bank" {
		t.Fatalf("enrollment = %#v", res.enrollment)
	}
	saved, err := teller.LoadEnrollment(enrollmentPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "token_e2e_secret" {
		t.Fatalf("saved enrollment = %#v", saved)
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
