package connect

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/dannyaziz/lasso-money/internal/teller"
)

type Options struct {
	ApplicationID  string
	Environment    string
	Port           int
	Timeout        time.Duration
	OpenBrowser    bool
	EnrollmentPath string
	Status         func(string)
	// OnURL receives the local Connect URL as soon as the server is
	// listening, so callers (e.g. agents) can relay it to a human.
	OnURL func(string)
}

const pageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8" />
<title>lasso — Teller Connect</title>
<script src="https://cdn.teller.io/connect/connect.js"></script>
<style>
body { font-family: ui-sans-serif, system-ui, sans-serif; max-width: 520px; margin: 8vh auto; padding: 0 1rem; color: #111; }
h1 { font-size: 1.25rem; }
button { font-size: 1rem; padding: .6rem 1.1rem; background: #111; color: #fff; border: 0; border-radius: 6px; cursor: pointer; }
button:hover { background: #333; }
code { background: #f5f5f5; padding: .1rem .3rem; border-radius: 3px; }
.ok { color: #0a7; }
.err { color: #b22; }
</style>
</head>
<body>
<h1>Link a bank through Teller</h1>
<p>Environment: <code>__ENV__</code>. Sandbox creds: <code>username</code> / <code>password</code>.</p>
<button id="go">Open Teller Connect</button>
<p id="status"></p>
<script>
const setup = TellerConnect.setup({
  applicationId: "__APPID__",
  environment: "__ENV__",
  onSuccess: async (enrollment) => {
    const el = document.getElementById("status");
    el.className = "ok";
    el.textContent = "Success — saving…";
    try {
      const resp = await fetch("/callback?token=__TOKEN__", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(enrollment),
      });
      if (!resp.ok) throw new Error("HTTP " + resp.status);
      el.textContent = "Saved. You can close this tab.";
    } catch (err) {
      el.className = "err";
      el.textContent = "Failed to save: " + err.message;
    }
  },
  onExit: () => {
    document.getElementById("status").className = "err";
    document.getElementById("status").textContent = "Cancelled.";
    fetch("/cancel?token=__TOKEN__", { method: "POST" });
  },
});
document.getElementById("go").addEventListener("click", () => setup.open());
</script>
</body>
</html>`

func Run(ctx context.Context, opts Options) (teller.Enrollment, error) {
	status := opts.Status
	if status == nil {
		status = func(string) {}
	}
	if opts.ApplicationID == "" {
		return teller.Enrollment{}, fmt.Errorf("TELLER_APPLICATION_ID is required")
	}
	if opts.Environment == "" {
		opts.Environment = "sandbox"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}

	listener, err := listen(opts.Port)
	if err != nil {
		return teller.Enrollment{}, err
	}
	defer listener.Close()

	token, err := randomToken()
	if err != nil {
		return teller.Enrollment{}, err
	}
	resultCh := make(chan connectResult, 1)
	server := &http.Server{Handler: handler(opts, token, resultCh)}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	url := "http://" + listener.Addr().String() + "/"
	if opts.OnURL != nil {
		opts.OnURL(url)
	}
	status("Open " + url + " in your browser to link a bank.")
	status("Environment: " + opts.Environment + ". Sandbox creds: username / password.")
	if opts.OpenBrowser {
		_ = openBrowser(url)
	}
	status("Waiting for you to finish in the browser…")

	timer := time.NewTimer(opts.Timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return teller.Enrollment{}, ctx.Err()
	case <-timer.C:
		return teller.Enrollment{}, fmt.Errorf("timed out waiting for Teller Connect")
	case res := <-resultCh:
		if res.err != nil {
			return teller.Enrollment{}, res.err
		}
		if err := teller.SaveEnrollment(opts.EnrollmentPath, res.enrollment); err != nil {
			return teller.Enrollment{}, err
		}
		return res.enrollment, nil
	}
}

type connectResult struct {
	enrollment teller.Enrollment
	err        error
}

func listen(port int) (net.Listener, error) {
	address := "127.0.0.1:8765"
	if port > 0 {
		address = fmt.Sprintf("127.0.0.1:%d", port)
	}
	listener, err := net.Listen("tcp", address)
	if err == nil || port > 0 {
		return listener, err
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

func handler(opts Options, token string, result chan<- connectResult) http.Handler {
	mux := http.NewServeMux()
	page := pageTemplate
	page = replace(page, "__APPID__", opts.ApplicationID)
	page = replace(page, "__ENV__", opts.Environment)
	page = replace(page, "__TOKEN__", token)
	// The one-time token plus the JSON content-type requirement stop other
	// local processes and cross-origin form posts (e.g. the text/plain JSON
	// smuggling trick) from planting or cancelling an enrollment.
	authorized := func(r *http.Request) bool {
		got := r.URL.Query().Get("token")
		return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
	}
	// Only the first result matters; never block a handler on the channel.
	deliver := func(res connectResult) {
		select {
		case result <- res:
		default:
		}
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authorized(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
			return
		}
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			deliver(connectResult{err: fmt.Errorf("invalid Connect JSON: %w", err)})
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		enrollment, err := teller.NormalizeConnectPayload(payload)
		if err != nil {
			deliver(connectResult{err: err})
			http.Error(w, "bad enrollment", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
		deliver(connectResult{enrollment: enrollment})
	})
	mux.HandleFunc("/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
		deliver(connectResult{err: fmt.Errorf("Teller Connect cancelled")})
	})
	return mux
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate connect token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func replace(s, old, value string) string {
	return strings.ReplaceAll(s, old, html.EscapeString(value))
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
