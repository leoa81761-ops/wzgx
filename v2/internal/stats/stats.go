// Package stats shows the WeChat article numbers pulled for verified accounts (see
// publish.StatsDue): a per-account page, a weekly summary, and the optional, visible weighting
// of column rotation by reads.
package stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/audit"
	"gonghao/internal/auth"
	"gonghao/internal/server"
)

var china = time.FixedZone("CST", 8*3600)

type Service struct {
	DB    *pgxpool.Pool
	Audit audit.Logger
	Now   func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) Register(srv *server.Server, h *auth.Handlers) {
	srv.Handle("GET /api/accounts/{id}/stats", h.Require(auth.UserOnly, http.HandlerFunc(s.hStats)))
	srv.Handle("PUT /api/accounts/{id}/stats/weighting", h.Require(auth.UserOnly, http.HandlerFunc(s.hWeighting)))
	srv.Handle("GET /api/me/weekly", h.Require(auth.UserOnly, http.HandlerFunc(s.hWeekly)))
}

var errNotFound = errors.New("不存在")

func who(r *http.Request) auth.User { u, _ := auth.FromContext(r.Context()); return u }
func pathID(r *http.Request) int64  { n, _ := strconv.ParseInt(r.PathValue("id"), 10, 64); return n }

func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, errNotFound) {
		server.WriteError(w, 404, "不存在")
		return
	}
	server.WriteError(w, 500, "服务器出错了")
}

func (s *Service) own(ctx context.Context, u auth.User, accID int64) error {
	var owner int64
	err := s.DB.QueryRow(ctx, `SELECT user_id FROM wx_account WHERE id=$1`, accID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != u.ID) {
		return errNotFound
	}
	return err
}

// Factor is how the weighting would change a column's share of the rotation.
type Factor struct {
	ColumnID int64   `json:"column_id"`
	Name     string  `json:"name"`
	Weight   int     `json:"weight"`   // the column's own weight
	Articles int     `json:"articles"` // published articles with numbers, last 60 days
	AvgReads float64 `json:"avg_reads"`
	Factor   float64 `json:"factor"` // 1 = unchanged
}

const (
	minPerColumn = 2
	minTotal     = 4
	minFactor    = 0.7
	maxFactor    = 1.5
)

// Factors: a column's factor is its average reads per article divided by the account's average,
// kept within 0.7–1.5 and rounded to 0.1. Columns with fewer than 2 measured articles (or an
// account with fewer than 4) stay at 1.
func Factors(ctx context.Context, db *pgxpool.Pool, accountID int64) ([]Factor, float64, error) {
	rows, err := db.Query(ctx, `WITH latest AS (
			SELECT DISTINCT ON (d.article_id) d.article_id, d.read_user FROM article_stat_day d
			JOIN article a ON a.id=d.article_id WHERE a.account_id=$1 AND a.wx_ref_date > current_date - 60
			ORDER BY d.article_id, d.stat_date DESC)
		SELECT c.id, c.name, c.weight, count(l.article_id)::int, COALESCE(sum(l.read_user),0)::float8
		FROM account_column c LEFT JOIN article a ON a.column_id=c.id LEFT JOIN latest l ON l.article_id=a.id
		WHERE c.account_id=$1 AND c.enabled GROUP BY c.id ORDER BY c.id`, accountID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Factor
	var n int
	var total float64
	for rows.Next() {
		var f Factor
		var sum float64
		if err := rows.Scan(&f.ColumnID, &f.Name, &f.Weight, &f.Articles, &sum); err != nil {
			return nil, 0, err
		}
		if f.Articles > 0 {
			f.AvgReads = math.Round(sum / float64(f.Articles))
		}
		n += f.Articles
		total += sum
		f.Factor = 1
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	avg := 0.0
	if n > 0 {
		avg = total / float64(n)
	}
	if n >= minTotal && avg > 0 {
		for i := range out {
			if out[i].Articles >= minPerColumn {
				f := math.Round(out[i].AvgReads/avg*10) / 10
				out[i].Factor = math.Max(minFactor, math.Min(maxFactor, f))
			}
		}
	}
	return out, math.Round(avg), nil
}

type day struct {
	Date   string `json:"date"`
	Reads  int    `json:"reads"`
	Shares int    `json:"shares"`
	Favs   int    `json:"favs"`
}

type art struct {
	ID         int64   `json:"id"`
	Title      string  `json:"title"`
	Column     string  `json:"column"`
	Published  string  `json:"published"`
	Reads      int     `json:"reads"`
	Shares     int     `json:"shares"`
	Favs       int     `json:"favs"`
	Likes      int     `json:"likes"`
	FinishRate float64 `json:"finish_rate"`
}

// daily: new reads/shares/favorites per day across the account (the cumulative numbers' increase).
func (s *Service) daily(ctx context.Context, where string, arg int64, from, to string) ([]day, error) {
	rows, err := s.DB.Query(ctx, `WITH x AS (
			SELECT d.stat_date, d.read_user - COALESCE(LAG(d.read_user) OVER w, 0) r,
				d.share_user - COALESCE(LAG(d.share_user) OVER w, 0) sh, d.collection_user - COALESCE(LAG(d.collection_user) OVER w, 0) f
			FROM article_stat_day d JOIN article a ON a.id=d.article_id JOIN wx_account acc ON acc.id=a.account_id
			WHERE `+where+`=$1 WINDOW w AS (PARTITION BY d.article_id ORDER BY d.stat_date))
		SELECT to_char(stat_date,'YYYY-MM-DD'), GREATEST(sum(r),0)::int, GREATEST(sum(sh),0)::int, GREATEST(sum(f),0)::int
		FROM x WHERE stat_date BETWEEN $2::date AND $3::date GROUP BY stat_date ORDER BY stat_date`, arg, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]day{}
	for rows.Next() {
		var d day
		if err := rows.Scan(&d.Date, &d.Reads, &d.Shares, &d.Favs); err != nil {
			return nil, err
		}
		by[d.Date] = d
	}
	var out []day
	t0, _ := time.Parse("2006-01-02", from)
	t1, _ := time.Parse("2006-01-02", to)
	for t := t0; !t.After(t1); t = t.AddDate(0, 0, 1) {
		k := t.Format("2006-01-02")
		d, ok := by[k]
		if !ok {
			d = day{Date: k}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Service) hStats(w http.ResponseWriter, r *http.Request) {
	ctx, accID := r.Context(), pathID(r)
	if err := s.own(ctx, who(r), accID); err != nil {
		fail(w, err)
		return
	}
	var verified, weighting bool
	var fetched, note, rot string
	var statsAt, rotAt *time.Time
	if err := s.DB.QueryRow(ctx, `SELECT verified, stats_weighting, COALESCE(to_char(stats_fetched_on,'YYYY-MM-DD'),''), stats_at, stats_note, rotation_note, rotation_at
		FROM wx_account WHERE id=$1`, accID).Scan(&verified, &weighting, &fetched, &statsAt, &note, &rot, &rotAt); err != nil {
		fail(w, err)
		return
	}
	now := s.now().In(china)
	to := now.AddDate(0, 0, -1).Format("2006-01-02")
	from := now.AddDate(0, 0, -30).Format("2006-01-02")
	days, err := s.daily(ctx, "a.account_id", accID, from, to)
	if err != nil {
		fail(w, err)
		return
	}
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT ON (a.id) a.id, a.title, COALESCE(c.name,''), to_char(a.wx_ref_date,'YYYY-MM-DD'),
			d.read_user, d.share_user, d.collection_user, d.like_user, d.finish_rate
		FROM article a JOIN article_stat_day d ON d.article_id=a.id LEFT JOIN account_column c ON c.id=a.column_id
		WHERE a.account_id=$1 ORDER BY a.id DESC, d.stat_date DESC LIMIT 100`, accID)
	if err != nil {
		fail(w, err)
		return
	}
	arts := []art{}
	for rows.Next() {
		var a art
		var fr float32
		if err := rows.Scan(&a.ID, &a.Title, &a.Column, &a.Published, &a.Reads, &a.Shares, &a.Favs, &a.Likes, &fr); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		a.FinishRate = math.Round(float64(fr)*1000) / 1000
		arts = append(arts, a)
	}
	rows.Close()
	sort.SliceStable(arts, func(i, j int) bool { return arts[i].Published > arts[j].Published })
	factors, avg, err := Factors(ctx, s.DB, accID)
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]any{"verified": verified, "weighting": weighting, "fetched_on": fetched, "stats_at": statsAt,
		"note": note, "rotation_note": rot, "rotation_at": rotAt, "days": days, "articles": arts, "columns": factors, "avg_reads": avg,
		"rule": fmt.Sprintf("每个栏目的权重 × 系数。系数 = 该栏目近 60 天每篇平均阅读 ÷ 全号平均，限制在 %.1f 到 %.1f 之间；栏目少于 %d 篇有数据、或全号少于 %d 篇时系数为 1。", minFactor, maxFactor, minPerColumn, minTotal)})
}

func (s *Service) hWeighting(w http.ResponseWriter, r *http.Request) {
	ctx, u, accID := r.Context(), who(r), pathID(r)
	if err := s.own(ctx, u, accID); err != nil {
		fail(w, err)
		return
	}
	var in struct {
		On bool `json:"on"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		server.WriteError(w, 400, "请求格式不正确")
		return
	}
	if _, err := s.DB.Exec(ctx, `UPDATE wx_account SET stats_weighting=$2, updated_at=now() WHERE id=$1`, accID, in.On); err != nil {
		fail(w, err)
		return
	}
	s.Audit.Log(ctx, u.ID, "account.stats_weighting", fmt.Sprint(accID), map[string]any{"on": in.On})
	server.WriteJSON(w, 200, map[string]bool{"on": in.On})
}

// hWeekly: last week's (the 7 days to yesterday) numbers across the user's accounts, as one short
// line for the notification area; empty when there is nothing to say.
func (s *Service) hWeekly(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), who(r)
	now := s.now().In(china)
	to := now.AddDate(0, 0, -1).Format("2006-01-02")
	from := now.AddDate(0, 0, -7).Format("2006-01-02")
	days, err := s.daily(ctx, "acc.user_id", u.ID, from, to)
	if err != nil {
		fail(w, err)
		return
	}
	var reads, shares, favs int
	for _, d := range days {
		reads, shares, favs = reads+d.Reads, shares+d.Shares, favs+d.Favs
	}
	if reads+shares+favs == 0 {
		server.WriteJSON(w, 200, map[string]string{"key": "", "text": ""})
		return
	}
	var title string
	var best int
	s.DB.QueryRow(ctx, `WITH x AS (SELECT d.article_id, d.stat_date, d.read_user - COALESCE(LAG(d.read_user) OVER (PARTITION BY d.article_id ORDER BY d.stat_date),0) r
			FROM article_stat_day d JOIN article a ON a.id=d.article_id WHERE a.user_id=$1)
		SELECT a.title, sum(x.r)::int FROM x JOIN article a ON a.id=x.article_id WHERE x.stat_date BETWEEN $2::date AND $3::date
		GROUP BY a.id, a.title ORDER BY 2 DESC LIMIT 1`, u.ID, from, to).Scan(&title, &best)
	var b strings.Builder
	fmt.Fprintf(&b, "上周（%s–%s）文章新增阅读 %d 人、分享 %d 人、收藏 %d 人。", from[5:], to[5:], reads, shares, favs)
	if best > 0 {
		fmt.Fprintf(&b, "读的人最多的是《%s》（%d 人）。", title, best)
	}
	server.WriteJSON(w, 200, map[string]string{"key": from, "text": b.String()})
}
