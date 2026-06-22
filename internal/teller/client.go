package teller

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.teller.io"

// maxTransactionPages bounds from_id pagination so a server that ignores
// from_id cannot loop forever. 40 pages × 500 = 20k transactions per window.
const maxTransactionPages = 40

type Client struct {
	baseURL    string
	httpClient *http.Client
	retryDelay []time.Duration
}

type Options struct {
	BaseURL  string
	Env      string
	CertPath string
	KeyPath  string
}

type Account struct {
	ID              string            `json:"id"`
	EnrollmentID    string            `json:"enrollment_id"`
	InstitutionID   string            `json:"institution_id,omitempty"`
	InstitutionName string            `json:"institution_name,omitempty"`
	Name            string            `json:"name,omitempty"`
	Type            string            `json:"type,omitempty"`
	Subtype         string            `json:"subtype,omitempty"`
	Currency        string            `json:"currency,omitempty"`
	LastFour        string            `json:"last_four,omitempty"`
	Status          string            `json:"status,omitempty"`
	Links           map[string]string `json:"links,omitempty"`
}

type Balance struct {
	AccountID string `json:"account_id"`
	Ledger    string `json:"ledger,omitempty"`
	Available string `json:"available,omitempty"`
}

type Transaction struct {
	ID             string         `json:"id"`
	AccountID      string         `json:"account_id"`
	Amount         string         `json:"amount"`
	Date           string         `json:"date"`
	Description    string         `json:"description,omitempty"`
	Status         string         `json:"status,omitempty"`
	Type           string         `json:"type,omitempty"`
	RunningBalance *string        `json:"running_balance,omitempty"`
	Details        map[string]any `json:"details,omitempty"`
}

type Error struct {
	StatusCode int
	Code       string
	Message    string
	Path       string
}

func (e Error) Error() string {
	if e.Code != "" || e.Message != "" {
		return fmt.Sprintf("Teller %d on %s: %s %s", e.StatusCode, e.Path, e.Code, e.Message)
	}
	return fmt.Sprintf("Teller %d on %s", e.StatusCode, e.Path)
}

func NewClient(opts Options) (*Client, error) {
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	env := opts.Env
	if env == "" {
		env = "sandbox"
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if opts.CertPath != "" || opts.KeyPath != "" {
		if opts.CertPath == "" || opts.KeyPath == "" {
			return nil, fmt.Errorf("both TELLER_CERT_PATH and TELLER_KEY_PATH are required when one is set")
		}
		cert, err := tls.LoadX509KeyPair(opts.CertPath, opts.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("load Teller client certificate: %w", err)
		}
		transport.TLSClientConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	} else if env != "sandbox" {
		return nil, fmt.Errorf("Teller %q requires mTLS cert/key; set TELLER_CERT_PATH and TELLER_KEY_PATH", env)
	}

	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
		retryDelay: []time.Duration{2 * time.Second, 5 * time.Second},
	}, nil
}

func (c *Client) ListAccounts(enrollment Enrollment) ([]Account, error) {
	var raw []struct {
		ID           string                    `json:"id"`
		EnrollmentID string                    `json:"enrollment_id"`
		Institution  struct{ ID, Name string } `json:"institution"`
		Name         string                    `json:"name"`
		Type         string                    `json:"type"`
		Subtype      string                    `json:"subtype"`
		Currency     string                    `json:"currency"`
		LastFour     string                    `json:"last_four"`
		Status       string                    `json:"status"`
		Links        map[string]string         `json:"links"`
	}
	if err := c.get("/accounts", nil, enrollment.AccessToken, &raw); err != nil {
		return nil, err
	}
	accounts := make([]Account, 0, len(raw))
	for _, r := range raw {
		if enrollment.ID != "" && r.EnrollmentID != "" && r.EnrollmentID != enrollment.ID {
			return nil, fmt.Errorf("account %s returned enrollment_id %q for enrollment %q", r.ID, r.EnrollmentID, enrollment.ID)
		}
		// A singleton legacy credential may have no enrollment ID. Keep its
		// account routing key blank so later calls use that same credential.
		enrollmentID := enrollment.ID
		accounts = append(accounts, Account{
			ID:              r.ID,
			EnrollmentID:    enrollmentID,
			InstitutionID:   r.Institution.ID,
			InstitutionName: r.Institution.Name,
			Name:            r.Name,
			Type:            r.Type,
			Subtype:         r.Subtype,
			Currency:        r.Currency,
			LastFour:        r.LastFour,
			Status:          r.Status,
			Links:           r.Links,
		})
	}
	return accounts, nil
}

func (c *Client) GetBalance(enrollment Enrollment, accountID string) (Balance, error) {
	var balance Balance
	if err := c.get("/accounts/"+url.PathEscape(accountID)+"/balances", nil, enrollment.AccessToken, &balance); err != nil {
		return Balance{}, err
	}
	return balance, nil
}

func (c *Client) ListTransactions(enrollment Enrollment, accountID, startDate, endDate string, count int) ([]Transaction, error) {
	if count <= 0 {
		count = 500
	}
	params := url.Values{}
	params.Set("count", fmt.Sprintf("%d", count))
	if startDate != "" {
		params.Set("start_date", startDate)
	}
	if endDate != "" {
		params.Set("end_date", endDate)
	}

	all := []Transaction{}
	for page := 0; ; page++ {
		if page >= maxTransactionPages {
			return nil, fmt.Errorf("transaction pagination exceeded %d pages for account %s; narrow the date window", maxTransactionPages, accountID)
		}
		var batch []Transaction
		if err := c.get("/accounts/"+url.PathEscape(accountID)+"/transactions", params, enrollment.AccessToken, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < count {
			break
		}
		params.Set("from_id", batch[len(batch)-1].ID)
	}
	return all, nil
}

// get retries on network errors and retryable Teller statuses (429/502/504);
// Teller documents that initial transaction fetches can time out and should
// be retried after a few seconds.
func (c *Client) get(path string, params url.Values, accessToken string, out any) error {
	var lastErr error
	for attempt := 0; attempt <= len(c.retryDelay); attempt++ {
		if attempt > 0 {
			time.Sleep(c.retryDelay[attempt-1])
		}
		err := c.getOnce(path, params, accessToken, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable(err) {
			return err
		}
	}
	return lastErr
}

func retryable(err error) bool {
	var tellerErr Error
	if errors.As(err, &tellerErr) {
		switch tellerErr.StatusCode {
		case 429, 502, 504:
			return true
		}
		return false
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

func (c *Client) getOnce(path string, params url.Values, accessToken string, out any) error {
	endpoint := c.baseURL + path
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(accessToken, "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var parsed struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &parsed)
		return Error{StatusCode: resp.StatusCode, Code: parsed.Error.Code, Message: parsed.Error.Message, Path: path}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode Teller response for %s: %w", path, err)
	}
	return nil
}
