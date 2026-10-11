package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
)

type PayOrder struct {
	ID              int64
	UserID          int64
	Account         string
	OutTradeNo      string
	Provider        string
	Channel         string
	Tier            string
	Period          string
	AmountCents     int
	Status          string
	CodeURL         string
	PayURL          string
	ProviderTradeNo string
	CreatedAt       time.Time
	PaidAt          *time.Time
}

func (s *Store) InsertPayOrder(ctx context.Context, q db.Q, o *PayOrder) error {
	if q == nil {
		q = s.DB
	}
	return q.QueryRow(ctx, `INSERT INTO pay_order (user_id, out_trade_no, provider, channel, tier, period, amount_cents, status, code_url, pay_url, provider_trade_no, created_at, paid_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		o.UserID, o.OutTradeNo, o.Provider, o.Channel, o.Tier, o.Period, o.AmountCents, o.Status, o.CodeURL, o.PayURL, o.ProviderTradeNo, timeReq(o.CreatedAt), timeArg(o.PaidAt),
	).Scan(&o.ID)
}

func (s *Store) GetPayOrder(ctx context.Context, q db.Q, outTradeNo string) (*PayOrder, error) {
	if q == nil {
		q = s.DB
	}
	row := q.QueryRow(ctx, `SELECT id, user_id, out_trade_no, provider, channel, tier, period, amount_cents, status, code_url, pay_url, provider_trade_no, created_at, paid_at FROM pay_order WHERE out_trade_no = ?`, outTradeNo)
	return scanPayOrder(row, "")
}

func (s *Store) GetPayOrderByID(ctx context.Context, q db.Q, id int64) (*PayOrder, error) {
	if q == nil {
		q = s.DB
	}
	row := q.QueryRow(ctx, `SELECT id, user_id, out_trade_no, provider, channel, tier, period, amount_cents, status, code_url, pay_url, provider_trade_no, created_at, paid_at FROM pay_order WHERE id = ?`, id)
	return scanPayOrder(row, "")
}

func (s *Store) ListPayOrders(ctx context.Context, limit int) ([]PayOrder, error) {
	if limit < 1 {
		limit = 100
	}
	rows, err := s.DB.Query(ctx, `SELECT o.id, o.user_id, o.out_trade_no, o.provider, o.channel, o.tier, o.period, o.amount_cents, o.status, o.code_url, o.pay_url, o.provider_trade_no, o.created_at, o.paid_at,
		COALESCE(u.email, u.phone, '') FROM pay_order o LEFT JOIN users u ON u.id = o.user_id ORDER BY o.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PayOrder
	for rows.Next() {
		item, err := scanPayOrder(rows, "account")
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	return out, rows.Err()
}

func (s *Store) SavePayLinks(ctx context.Context, q db.Q, id int64, codeURL, payURL string) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE pay_order SET code_url = ?, pay_url = ? WHERE id = ?`, codeURL, payURL, id)
	return err
}

func (s *Store) FailPayOrder(ctx context.Context, q db.Q, id int64) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `UPDATE pay_order SET status = 'failed' WHERE id = ? AND status = 'pending'`, id)
	return err
}

// FinishPayOrder 把 pending 改成 paid。返回 false 表示没有改到行。
func (s *Store) FinishPayOrder(ctx context.Context, q db.Q, outTradeNo, tradeNo string, paidAt time.Time) (bool, error) {
	if q == nil {
		q = s.DB
	}
	res, err := q.Exec(ctx, `UPDATE pay_order SET status = 'paid', provider_trade_no = ?, paid_at = ? WHERE out_trade_no = ? AND status = 'pending'`, tradeNo, timeReq(paidAt), outTradeNo)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RefundPayOrder 只改订单状态，不改会员档位。
func (s *Store) RefundPayOrder(ctx context.Context, q db.Q, id int64) (bool, error) {
	if q == nil {
		q = s.DB
	}
	res, err := q.Exec(ctx, `UPDATE pay_order SET status = 'refunded' WHERE id = ? AND status = 'paid'`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) InsertPaymentEntitlement(ctx context.Context, q db.Q, userID int64, tier, note, expires, created string) error {
	if q == nil {
		q = s.DB
	}
	var exp any
	if expires != "" {
		exp = expires
	}
	_, err := q.Exec(ctx, `INSERT INTO entitlement (user_id, source, tier, expires_at, admin_id, note, created_at) VALUES (?, 'payment', ?, ?, NULL, ?, ?)`,
		userID, tier, exp, note, created)
	return err
}

type payScanner interface {
	Scan(dest ...any) error
}

func scanPayOrder(row payScanner, accountMode string) (*PayOrder, error) {
	var o PayOrder
	var created string
	var paid sql.NullString
	var err error
	if accountMode == "account" {
		err = row.Scan(&o.ID, &o.UserID, &o.OutTradeNo, &o.Provider, &o.Channel, &o.Tier, &o.Period, &o.AmountCents, &o.Status, &o.CodeURL, &o.PayURL, &o.ProviderTradeNo, &created, &paid, &o.Account)
	} else {
		err = row.Scan(&o.ID, &o.UserID, &o.OutTradeNo, &o.Provider, &o.Channel, &o.Tier, &o.Period, &o.AmountCents, &o.Status, &o.CodeURL, &o.PayURL, &o.ProviderTradeNo, &created, &paid)
	}
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o.CreatedAt = mustTime(created)
	o.PaidAt = parsePtr(paid)
	return &o, nil
}
