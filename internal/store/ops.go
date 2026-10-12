package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/db"
)

type Notification struct {
	ID        int64
	UserID    int64
	Kind      string
	Title     string
	Body      string
	ReadAt    *time.Time
	CreatedAt time.Time
}

type Feedback struct {
	ID          int64
	UserID      int64
	ArticleID   int64
	Kind        string
	Score       int
	Note        string
	DraftState  string
	ChangeRatio float64
	CreatedAt   time.Time
}

type Complaint struct {
	ID        int64
	UserID    *int64
	Contact   string
	Kind      string
	Content   string
	Status    string
	Reply     string
	CreatedAt time.Time
	RepliedAt *time.Time
}

type TaskRow struct {
	ID            int64
	UserID        int64
	PlanID        int64
	Status        string
	TriggerType   string
	FailureReason string
	DurationMs    int
	CreatedAt     time.Time
}

func (s *Store) InsertNotification(ctx context.Context, q db.Q, n *Notification) error {
	if q == nil {
		q = s.DB
	}
	return q.QueryRow(ctx, `INSERT INTO notification (user_id, kind, title, body, created_at) VALUES (?, ?, ?, ?, ?) RETURNING id`,
		n.UserID, n.Kind, n.Title, n.Body, timeReq(n.CreatedAt)).Scan(&n.ID)
}

func (s *Store) ListNotifications(ctx context.Context, userID int64, limit int) ([]Notification, int, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	var unread int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM notification WHERE user_id = ? AND read_at IS NULL`, userID).Scan(&unread); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.Query(ctx, `SELECT id, user_id, kind, title, body, read_at, created_at FROM notification WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		var read sql.NullString
		var created string
		if err := rows.Scan(&n.ID, &n.UserID, &n.Kind, &n.Title, &n.Body, &read, &created); err != nil {
			return nil, 0, err
		}
		n.ReadAt = parsePtr(read)
		n.CreatedAt = mustTime(created)
		out = append(out, n)
	}
	return out, unread, rows.Err()
}

func (s *Store) MarkNotificationRead(ctx context.Context, userID, id int64, at time.Time) error {
	res, err := s.DB.Exec(ctx, `UPDATE notification SET read_at = ? WHERE id = ? AND user_id = ? AND read_at IS NULL`, timeReq(at), id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var exists int
		if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM notification WHERE id = ? AND user_id = ?`, id, userID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return sql.ErrNoRows
		}
	}
	return nil
}

func (s *Store) InsertFeedback(ctx context.Context, q db.Q, f *Feedback) error {
	if q == nil {
		q = s.DB
	}
	return q.QueryRow(ctx, `INSERT INTO feedback (user_id, article_id, kind, score, note, draft_state, change_ratio, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		f.UserID, f.ArticleID, f.Kind, f.Score, f.Note, f.DraftState, f.ChangeRatio, timeReq(f.CreatedAt)).Scan(&f.ID)
}

func (s *Store) ListFeedbackByUser(ctx context.Context, userID int64) ([]Feedback, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, user_id, article_id, kind, score, note, draft_state, change_ratio, created_at FROM feedback WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Feedback
	for rows.Next() {
		f, err := scanFeedback(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanFeedback(row interface{ Scan(...any) error }) (Feedback, error) {
	var f Feedback
	var created string
	err := row.Scan(&f.ID, &f.UserID, &f.ArticleID, &f.Kind, &f.Score, &f.Note, &f.DraftState, &f.ChangeRatio, &created)
	f.CreatedAt = mustTime(created)
	return f, err
}

func (s *Store) InsertComplaint(ctx context.Context, c *Complaint) error {
	var user any
	if c.UserID != nil {
		user = *c.UserID
	}
	return s.DB.QueryRow(ctx, `INSERT INTO complaint (user_id, contact, kind, content, status, reply, created_at) VALUES (?, ?, ?, ?, ?, '', ?) RETURNING id`,
		user, c.Contact, c.Kind, c.Content, "open", timeReq(c.CreatedAt)).Scan(&c.ID)
}

func (s *Store) ListComplaints(ctx context.Context, limit int) ([]Complaint, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, `SELECT id, user_id, contact, kind, content, status, reply, created_at, replied_at FROM complaint ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Complaint
	for rows.Next() {
		c, err := scanComplaint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanComplaint(row interface{ Scan(...any) error }) (Complaint, error) {
	var c Complaint
	var user sql.NullInt64
	var created string
	var replied sql.NullString
	err := row.Scan(&c.ID, &user, &c.Contact, &c.Kind, &c.Content, &c.Status, &c.Reply, &created, &replied)
	if user.Valid {
		id := user.Int64
		c.UserID = &id
	}
	c.CreatedAt = mustTime(created)
	c.RepliedAt = parsePtr(replied)
	return c, err
}

func (s *Store) ReplyComplaint(ctx context.Context, id int64, status, reply string, at time.Time) error {
	res, err := s.DB.Exec(ctx, `UPDATE complaint SET status = ?, reply = ?, replied_at = ? WHERE id = ?`, status, reply, timeReq(at), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ListVetoTags(ctx context.Context, q db.Q, planID int64) ([]string, error) {
	if q == nil {
		q = s.DB
	}
	rows, err := q.Query(ctx, `SELECT topic_tag FROM topic_veto WHERE plan_id = ? ORDER BY id ASC`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		out = append(out, tag)
	}
	return out, rows.Err()
}

func (s *Store) AddVeto(ctx context.Context, planID int64, tag string, at time.Time) error {
	return s.InsertVeto(ctx, nil, planID, tag, at)
}

func (s *Store) InsertVeto(ctx context.Context, q db.Q, planID int64, tag string, at time.Time) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `INSERT INTO topic_veto (plan_id, topic_tag, created_at) VALUES (?, ?, ?)`, planID, tag, timeReq(at))
	if db.IsUnique(err) {
		return nil
	}
	return err
}

func (s *Store) RemoveVeto(ctx context.Context, planID int64, tag string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM topic_veto WHERE plan_id = ? AND topic_tag = ?`, planID, tag)
	return err
}

func (s *Store) GetPin(ctx context.Context, q db.Q, planID int64) (string, error) {
	if q == nil {
		q = s.DB
	}
	var title string
	err := q.QueryRow(ctx, `SELECT specified_title FROM plan_pin WHERE plan_id = ?`, planID).Scan(&title)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return title, err
}

func (s *Store) SetPin(ctx context.Context, planID int64, title string, at time.Time) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO plan_pin (plan_id, specified_title, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(plan_id) DO UPDATE SET specified_title = excluded.specified_title, updated_at = excluded.updated_at`, planID, title, timeReq(at))
	return err
}

func (s *Store) ClearPin(ctx context.Context, q db.Q, planID int64) error {
	if q == nil {
		q = s.DB
	}
	_, err := q.Exec(ctx, `DELETE FROM plan_pin WHERE plan_id = ?`, planID)
	return err
}

func (s *Store) ListRecentTasks(ctx context.Context, limit int) ([]TaskRow, error) {
	if limit < 1 || limit > 100 {
		limit = 40
	}
	rows, err := s.DB.Query(ctx, `SELECT id, user_id, plan_id, status, trigger_type, failure_reason, duration_ms, created_at FROM article_task ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskRow
	for rows.Next() {
		var row TaskRow
		var created string
		if err := rows.Scan(&row.ID, &row.UserID, &row.PlanID, &row.Status, &row.TriggerType, &row.FailureReason, &row.DurationMs, &created); err != nil {
			return nil, err
		}
		row.CreatedAt = mustTime(created)
		out = append(out, row)
	}
	return out, rows.Err()
}
