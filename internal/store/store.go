package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dannyaziz/lasso-money/internal/teller"
)

type Store struct {
	db *sql.DB
}

type TxFilter struct {
	AccountID string
	From      string
	To        string
	Query     string
	Status    string
	MinAmount string
	MaxAmount string
	Category  string
	Merchant  string
	Limit     int
}

type TransactionRow struct {
	ID               string  `json:"id"`
	AccountID        string  `json:"account_id"`
	AccountName      string  `json:"account_name,omitempty"`
	AccountLastFour  string  `json:"account_last_four,omitempty"`
	Amount           string  `json:"amount"`
	Currency         string  `json:"currency,omitempty"`
	Date             string  `json:"date"`
	Description      string  `json:"description,omitempty"`
	CounterpartyName string  `json:"counterparty_name,omitempty"`
	Category         string  `json:"category,omitempty"`
	Status           string  `json:"status,omitempty"`
	Type             string  `json:"type,omitempty"`
	RunningBalance   *string `json:"running_balance,omitempty"`
}

type SpendRow struct {
	Group    string  `json:"group"`
	Spend    float64 `json:"spend"`
	Count    int     `json:"count"`
	Currency string  `json:"currency,omitempty"`
}

type CashflowRow struct {
	Month    string  `json:"month"`
	Inflow   float64 `json:"inflow"`
	Outflow  float64 `json:"outflow"`
	Net      float64 `json:"net"`
	Count    int     `json:"count"`
	Currency string  `json:"currency,omitempty"`
}

type CacheSummary struct {
	Counts         map[string]int `json:"counts"`
	LastSyncAt     string         `json:"last_sync_at,omitempty"`
	LastSyncStart  string         `json:"last_sync_start,omitempty"`
	LastSyncEnd    string         `json:"last_sync_end,omitempty"`
	LastSyncStatus string         `json:"last_sync_status,omitempty"`
}

const removedAccountStatus = "__lasso_removed"

type BalanceRow struct {
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
	LastFour  string `json:"last_four,omitempty"`
	Currency  string `json:"currency,omitempty"`
	Ledger    string `json:"ledger,omitempty"`
	Available string `json:"available,omitempty"`
	AsOf      string `json:"as_of"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// PRAGMAs are per-connection; cap the pool at one connection so they
	// apply to every statement. SQLite is single-writer anyway.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Migrate() error {
	_, err := s.db.Exec(schema)
	return err
}

func (s *Store) UpsertAccounts(accounts []teller.Account) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO accounts (id,enrollment_id,institution_id,institution_name,name,type,subtype,currency,last_four,status)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET enrollment_id=excluded.enrollment_id,institution_id=excluded.institution_id,institution_name=excluded.institution_name,name=excluded.name,type=excluded.type,subtype=excluded.subtype,currency=excluded.currency,last_four=excluded.last_four,status=excluded.status`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, a := range accounts {
		if _, err := stmt.Exec(a.ID, a.EnrollmentID, a.InstitutionID, a.InstitutionName, a.Name, a.Type, a.Subtype, a.Currency, a.LastFour, a.Status); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertBalance(account teller.Account, balance teller.Balance) error {
	_, err := s.db.Exec(`INSERT INTO balances (account_id,ledger,available,as_of)
		VALUES (?,?,?,?)
		ON CONFLICT(account_id) DO UPDATE SET ledger=excluded.ledger,available=excluded.available,as_of=excluded.as_of`, account.ID, balance.Ledger, balance.Available, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) CachedBalances(accountIDs []string) ([]BalanceRow, error) {
	if accountIDs != nil && len(accountIDs) == 0 {
		return []BalanceRow{}, nil
	}
	query := `SELECT a.id,coalesce(a.name,''),coalesce(a.last_four,''),coalesce(a.currency,''),coalesce(b.ledger,''),coalesce(b.available,''),coalesce(b.as_of,'')
		FROM accounts a LEFT JOIN balances b ON b.account_id=a.id WHERE coalesce(a.status,'') != ?`
	args := []any{removedAccountStatus}
	if accountIDs != nil {
		query += " AND a.id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(accountIDs)), ",") + ")"
		for _, id := range accountIDs {
			args = append(args, id)
		}
	}
	rows, err := s.db.Query(query+" ORDER BY a.name", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BalanceRow{}
	for rows.Next() {
		var row BalanceRow
		if err := rows.Scan(&row.AccountID, &row.Name, &row.LastFour, &row.Currency, &row.Ledger, &row.Available, &row.AsOf); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) PruneBalances(accountIDs []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(accountIDs) == 0 {
		if _, err := tx.Exec(`DELETE FROM balances`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE accounts SET status=?`, removedAccountStatus); err != nil {
			return err
		}
		return tx.Commit()
	}
	args := make([]any, len(accountIDs))
	for i, id := range accountIDs {
		args[i] = id
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(accountIDs)), ",")
	if _, err := tx.Exec("DELETE FROM balances WHERE account_id NOT IN ("+placeholders+")", args...); err != nil {
		return err
	}
	statusArgs := append([]any{removedAccountStatus}, args...)
	if _, err := tx.Exec("UPDATE accounts SET status=? WHERE id NOT IN ("+placeholders+")", statusArgs...); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertTransactions(account teller.Account, txs []teller.Transaction) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO transactions (id,account_id,amount,currency,date,description,counterparty_name,category,status,type,running_balance)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET account_id=excluded.account_id,amount=excluded.amount,currency=excluded.currency,date=excluded.date,description=excluded.description,counterparty_name=excluded.counterparty_name,category=excluded.category,status=excluded.status,type=excluded.type,running_balance=excluded.running_balance`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, t := range txs {
		cp := counterpartyName(t.Details)
		cat := detailString(t.Details, "category")
		rb := ""
		if t.RunningBalance != nil {
			rb = *t.RunningBalance
		}
		if _, err := stmt.Exec(t.ID, account.ID, t.Amount, account.Currency, t.Date, t.Description, cp, cat, t.Status, t.Type, rb); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) StartSyncRun(accountID, startDate, endDate string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO sync_runs (started_at,account_id,start_date,end_date,status) VALUES (CURRENT_TIMESTAMP,?,?,?,'running')`, accountID, startDate, endDate)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FinishSyncRun(id int64, status string, fetched int) error {
	_, err := s.db.Exec(`UPDATE sync_runs SET finished_at=CURRENT_TIMESTAMP,status=?,fetched_count=? WHERE id=?`, status, fetched, id)
	return err
}

func (s *Store) IncrementalStartDate(accountID string, overlapDays int, fallbackDays int) (string, error) {
	// Anchor on the last successful run's end date so the window advances;
	// anchoring on start_date would re-fetch the full history every sync.
	var last string
	err := s.db.QueryRow(`SELECT coalesce(max(end_date),'') FROM sync_runs WHERE account_id=? AND status='ok'`, accountID).Scan(&last)
	if err != nil {
		return "", err
	}
	if last == "" {
		return time.Now().AddDate(0, 0, -fallbackDays).Format(time.DateOnly), nil
	}
	parsed, err := time.Parse(time.DateOnly, last)
	if err != nil {
		return time.Now().AddDate(0, 0, -fallbackDays).Format(time.DateOnly), nil
	}
	return parsed.AddDate(0, 0, -overlapDays).Format(time.DateOnly), nil
}

func (s *Store) CachedAccounts() ([]teller.Account, error) {
	rows, err := s.db.Query(`SELECT id,enrollment_id,institution_id,institution_name,coalesce(name,''),coalesce(type,''),coalesce(subtype,''),coalesce(currency,''),coalesce(last_four,''),coalesce(status,'') FROM accounts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []teller.Account{}
	for rows.Next() {
		var a teller.Account
		if err := rows.Scan(&a.ID, &a.EnrollmentID, &a.InstitutionID, &a.InstitutionName, &a.Name, &a.Type, &a.Subtype, &a.Currency, &a.LastFour, &a.Status); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) QueryTransactions(f TxFilter) ([]TransactionRow, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.AccountID != "" {
		where = append(where, "t.account_id = ?")
		args = append(args, f.AccountID)
	}
	if f.From != "" {
		where = append(where, "t.date >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, "t.date <= ?")
		args = append(args, f.To)
	}
	if f.Status != "" {
		where = append(where, "t.status = ?")
		args = append(args, f.Status)
	}
	if f.MinAmount != "" {
		where = append(where, "cast(t.amount as real) >= ?")
		args = append(args, f.MinAmount)
	}
	if f.MaxAmount != "" {
		where = append(where, "cast(t.amount as real) <= ?")
		args = append(args, f.MaxAmount)
	}
	if f.Category != "" {
		where = append(where, "lower(t.category) LIKE ?")
		args = append(args, "%"+strings.ToLower(f.Category)+"%")
	}
	if f.Merchant != "" {
		where = append(where, "(lower(t.counterparty_name) LIKE ? OR lower(t.description) LIKE ?)")
		m := "%" + strings.ToLower(f.Merchant) + "%"
		args = append(args, m, m)
	}
	if f.Query != "" {
		where = append(where, "(lower(t.description) LIKE ? OR lower(t.counterparty_name) LIKE ? OR lower(t.category) LIKE ?)")
		q := "%" + strings.ToLower(f.Query) + "%"
		args = append(args, q, q, q)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	q := `SELECT t.id,t.account_id,coalesce(a.name,''),coalesce(a.last_four,''),t.amount,coalesce(t.currency,a.currency,''),t.date,coalesce(t.description,''),coalesce(t.counterparty_name,''),coalesce(t.category,''),coalesce(t.status,''),coalesce(t.type,''),nullif(t.running_balance,'')
		FROM transactions t LEFT JOIN accounts a ON a.id=t.account_id WHERE ` + strings.Join(where, " AND ") + ` ORDER BY t.date DESC, t.id DESC LIMIT ?`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TransactionRow{}
	for rows.Next() {
		var r TransactionRow
		if err := rows.Scan(&r.ID, &r.AccountID, &r.AccountName, &r.AccountLastFour, &r.Amount, &r.Currency, &r.Date, &r.Description, &r.CounterpartyName, &r.Category, &r.Status, &r.Type, &r.RunningBalance); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// signedOutflow normalizes Teller's account-perspective amounts so positive
// always means money out: credit-account charges are already positive, while
// depository debits arrive negative and must be flipped. Accounts with an
// unknown type keep the credit convention.
const signedOutflow = "(CASE WHEN a.type = 'depository' THEN -cast(t.amount AS real) ELSE cast(t.amount AS real) END)"

func (s *Store) Spend(groupBy, from, to string, limit int) ([]SpendRow, error) {
	var expr string
	switch groupBy {
	case "merchant", "counterparty", "":
		expr = "coalesce(nullif(counterparty_name,''), nullif(description,''), 'unknown')"
	case "category":
		expr = "coalesce(nullif(category,''), 'uncategorized')"
	case "account":
		expr = "coalesce(a.name, t.account_id)"
	case "month":
		expr = "substr(t.date,1,7)"
	default:
		return nil, fmt.Errorf("unsupported --group %q; use merchant, category, account, or month", groupBy)
	}
	where := []string{signedOutflow + " > 0"}
	args := []any{}
	if from != "" {
		where = append(where, "t.date >= ?")
		args = append(args, from)
	}
	if to != "" {
		where = append(where, "t.date <= ?")
		args = append(args, to)
	}
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit)
	q := fmt.Sprintf(`SELECT %s AS grp, sum(%s) AS spend, count(*), coalesce(max(t.currency), max(a.currency), '')
		FROM transactions t LEFT JOIN accounts a ON a.id=t.account_id WHERE %s GROUP BY grp ORDER BY spend DESC LIMIT ?`, expr, signedOutflow, strings.Join(where, " AND "))
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SpendRow{}
	for rows.Next() {
		var r SpendRow
		if err := rows.Scan(&r.Group, &r.Spend, &r.Count, &r.Currency); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Cashflow(from, to string) ([]CashflowRow, error) {
	where := []string{"1=1"}
	args := []any{}
	if from != "" {
		where = append(where, "t.date >= ?")
		args = append(args, from)
	}
	if to != "" {
		where = append(where, "t.date <= ?")
		args = append(args, to)
	}
	q := `SELECT substr(t.date,1,7) AS month,
		sum(CASE WHEN ` + signedOutflow + ` < 0 THEN -` + signedOutflow + ` ELSE 0 END) AS inflow,
		sum(CASE WHEN ` + signedOutflow + ` > 0 THEN ` + signedOutflow + ` ELSE 0 END) AS outflow,
		sum(-` + signedOutflow + `) AS net,
		count(*), coalesce(max(t.currency), max(a.currency), '')
		FROM transactions t LEFT JOIN accounts a ON a.id=t.account_id WHERE ` + strings.Join(where, " AND ") + ` GROUP BY month ORDER BY month DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CashflowRow{}
	for rows.Next() {
		var r CashflowRow
		if err := rows.Scan(&r.Month, &r.Inflow, &r.Outflow, &r.Net, &r.Count, &r.Currency); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CacheSummary() (CacheSummary, error) {
	counts := map[string]int{}
	for _, table := range []string{"accounts", "balances", "transactions", "sync_runs"} {
		var count int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			return CacheSummary{}, err
		}
		counts[table] = count
	}
	summary := CacheSummary{Counts: counts}
	_ = s.db.QueryRow(`SELECT coalesce(finished_at,''), coalesce(start_date,''), coalesce(end_date,''), coalesce(status,'') FROM sync_runs ORDER BY id DESC LIMIT 1`).Scan(&summary.LastSyncAt, &summary.LastSyncStart, &summary.LastSyncEnd, &summary.LastSyncStatus)
	return summary, nil
}

func counterpartyName(details map[string]any) string {
	counterparty, ok := details["counterparty"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := counterparty["name"].(string)
	return name
}

func detailString(details map[string]any, key string) string {
	v, _ := details[key].(string)
	return v
}

const schema = `
CREATE TABLE IF NOT EXISTS accounts (
  id TEXT PRIMARY KEY,
  enrollment_id TEXT,
  institution_id TEXT,
  institution_name TEXT,
  name TEXT,
  type TEXT,
  subtype TEXT,
  currency TEXT,
  last_four TEXT,
  status TEXT
);
CREATE TABLE IF NOT EXISTS balances (
  account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  ledger TEXT,
  available TEXT,
  as_of TEXT
);
CREATE TABLE IF NOT EXISTS transactions (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  amount TEXT NOT NULL,
  currency TEXT,
  date TEXT NOT NULL,
  description TEXT,
  counterparty_name TEXT,
  category TEXT,
  status TEXT,
  type TEXT,
  running_balance TEXT
);
CREATE INDEX IF NOT EXISTS idx_transactions_account_date ON transactions(account_id, date);
CREATE INDEX IF NOT EXISTS idx_transactions_date ON transactions(date);
CREATE INDEX IF NOT EXISTS idx_transactions_counterparty ON transactions(counterparty_name);
CREATE INDEX IF NOT EXISTS idx_transactions_category ON transactions(category);
CREATE INDEX IF NOT EXISTS idx_transactions_status ON transactions(status);
CREATE TABLE IF NOT EXISTS sync_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  account_id TEXT,
  start_date TEXT,
  end_date TEXT,
  status TEXT,
  fetched_count INTEGER DEFAULT 0
);
`
