package account

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"gonghao/internal/auth"
)

// ArticleType is admin-managed. A column picks exactly one; types are independent.
type ArticleType struct {
	ID          int64     `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	NeedsNews   bool      `json:"needs_news"`
	Research    *bool     `json:"research"` // 是否检索资料; omitted = on for new types, unchanged on edit
	Enabled     bool      `json:"enabled"`
	Sort        int       `json:"sort"`
	CreatedAt   time.Time `json:"created_at"`
}

var typeKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)

func (t *ArticleType) validate() error {
	t.Key, t.Name = strings.TrimSpace(t.Key), strings.TrimSpace(t.Name)
	if !typeKeyRe.MatchString(t.Key) {
		return errors.New("类型标识只能用小写字母、数字和下划线")
	}
	if t.Name == "" || len([]rune(t.Name)) > 32 {
		return errors.New("请填写类型名称（32 字以内）")
	}
	if len([]rune(t.Description)) > maxField {
		return errors.New("说明过长")
	}
	return nil
}

const typeCols = `id, key, name, description, needs_news, research, enabled, sort, created_at`

func scanType(r pgx.Row) (ArticleType, error) {
	var t ArticleType
	err := r.Scan(&t.ID, &t.Key, &t.Name, &t.Description, &t.NeedsNews, &t.Research, &t.Enabled, &t.Sort, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// ListTypes: users see enabled types; staff see all.
func (s *Service) ListTypes(ctx context.Context, actor auth.User) ([]ArticleType, error) {
	q := `SELECT ` + typeCols + ` FROM article_type`
	if !actor.Role.IsStaff() {
		q += ` WHERE enabled`
	}
	rows, err := s.DB.Query(ctx, q+` ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArticleType{}
	for rows.Next() {
		t, err := scanType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Service) SaveType(ctx context.Context, actor auth.User, t ArticleType) (ArticleType, error) {
	if !actor.Role.IsStaff() {
		return t, ErrForbidden
	}
	if err := t.validate(); err != nil {
		return t, err
	}
	var err error
	var out ArticleType
	if t.ID == 0 {
		out, err = scanType(s.DB.QueryRow(ctx, `INSERT INTO article_type (key, name, description, needs_news, research, enabled, sort)
			VALUES ($1,$2,$3,$4,COALESCE($7,TRUE),$5,$6) RETURNING `+typeCols, t.Key, t.Name, t.Description, t.NeedsNews, t.Enabled, t.Sort, t.Research))
	} else {
		out, err = scanType(s.DB.QueryRow(ctx, `UPDATE article_type SET key=$2, name=$3, description=$4, needs_news=$5, enabled=$6, sort=$7,
			research=COALESCE($8, research) WHERE id=$1 RETURNING `+typeCols, t.ID, t.Key, t.Name, t.Description, t.NeedsNews, t.Enabled, t.Sort, t.Research))
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return t, errors.New("类型标识已存在")
	}
	if err != nil {
		return t, err
	}
	s.Audit.Log(ctx, actor.ID, "article_type.save", out.Key, map[string]any{"id": out.ID, "enabled": out.Enabled})
	return out, nil
}

type Column struct {
	ID            int64     `json:"id"`
	AccountID     int64     `json:"account_id"`
	Name          string    `json:"name"`
	ArticleTypeID int64     `json:"article_type_id"`
	Note          string    `json:"note"`
	LengthHint    *int      `json:"length_hint"` // reference only, never enforced
	Weight        int       `json:"weight"`
	Enabled       bool      `json:"enabled"`
	UpdatedAt     time.Time `json:"updated_at"`
}

const colCols = `id, account_id, name, article_type_id, note, length_hint, weight, enabled, updated_at`

func scanCol(r pgx.Row) (Column, error) {
	var c Column
	err := r.Scan(&c.ID, &c.AccountID, &c.Name, &c.ArticleTypeID, &c.Note, &c.LengthHint, &c.Weight, &c.Enabled, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

func (s *Service) ListColumns(ctx context.Context, actor auth.User, accountID int64) ([]Column, error) {
	if _, err := s.Get(ctx, actor, accountID); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT `+colCols+` FROM account_column WHERE account_id=$1 ORDER BY id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		c, err := scanCol(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) validateColumn(ctx context.Context, c *Column) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || len([]rune(c.Name)) > 32 {
		return errors.New("请填写栏目名称（32 字以内）")
	}
	if len([]rune(c.Note)) > maxField {
		return errors.New("栏目说明过长")
	}
	if c.Weight <= 0 {
		c.Weight = 1
	}
	if c.LengthHint != nil && *c.LengthHint <= 0 {
		c.LengthHint = nil
	}
	var enabled bool
	err := s.DB.QueryRow(ctx, `SELECT enabled FROM article_type WHERE id=$1`, c.ArticleTypeID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !enabled) {
		return errors.New("请选择一个可用的文章类型")
	}
	return err
}

// SaveColumn creates (ID=0) or updates a column on an account the actor may edit.
func (s *Service) SaveColumn(ctx context.Context, actor auth.User, accountID int64, c Column) (Column, error) {
	a, err := s.Get(ctx, actor, accountID)
	if err != nil {
		return c, err
	}
	if err := s.validateColumn(ctx, &c); err != nil {
		return c, err
	}
	var out Column
	if c.ID == 0 {
		out, err = scanCol(s.DB.QueryRow(ctx, `INSERT INTO account_column (account_id, name, article_type_id, note, length_hint, weight, enabled)
			VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+colCols, accountID, c.Name, c.ArticleTypeID, c.Note, c.LengthHint, c.Weight, c.Enabled))
	} else {
		out, err = scanCol(s.DB.QueryRow(ctx, `UPDATE account_column SET name=$3, article_type_id=$4, note=$5, length_hint=$6, weight=$7, enabled=$8, updated_at=now()
			WHERE id=$1 AND account_id=$2 RETURNING `+colCols, c.ID, accountID, c.Name, c.ArticleTypeID, c.Note, c.LengthHint, c.Weight, c.Enabled))
	}
	if err != nil {
		return c, err
	}
	s.Audit.Log(ctx, actor.ID, "column.save", a.AppID, map[string]any{"account_id": accountID, "column_id": out.ID, "article_type_id": out.ArticleTypeID})
	return out, nil
}

func (s *Service) DeleteColumn(ctx context.Context, actor auth.User, accountID, columnID int64) error {
	a, err := s.Get(ctx, actor, accountID)
	if err != nil {
		return err
	}
	t, err := s.DB.Exec(ctx, `DELETE FROM account_column WHERE id=$1 AND account_id=$2`, columnID, accountID)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23503" {
		// referenced by jobs/articles: keep history, just disable
		_, err = s.DB.Exec(ctx, `UPDATE account_column SET enabled=false, updated_at=now() WHERE id=$1 AND account_id=$2`, columnID, accountID)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if t.RowsAffected() == 0 {
		return ErrNotFound
	}
	s.Audit.Log(ctx, actor.ID, "column.delete", a.AppID, map[string]any{"account_id": accountID, "column_id": columnID})
	return nil
}
