package job

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"gonghao/internal/auth"
	"gonghao/internal/render"
	"gonghao/internal/server"
)

// Renderer re-renders stored articles (implemented by gen.Pipeline).
type Renderer interface {
	RenderStored(ctx context.Context, articleID int64, layout string) (string, error)
	Rerender(ctx context.Context, accountID int64) (int, error)
}

type layoutInfo struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Note string `json:"note"`
}

func layouts() []layoutInfo {
	out := []layoutInfo{}
	for _, l := range render.Layouts {
		out = append(out, layoutInfo{l.Key, l.Name, l.Note})
	}
	return out
}

func (s *Service) hGetLayout(w http.ResponseWriter, r *http.Request) {
	id := pid(r, "id")
	if err := s.ownAccount(r.Context(), actor(r), id); err != nil {
		fail(w, err)
		return
	}
	var l string
	if err := s.DB.QueryRow(r.Context(), `SELECT layout FROM wx_account WHERE id=$1`, id).Scan(&l); err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]any{"layout": l, "layouts": layouts()})
}

// SetLayout picks the account's template and re-renders its articles not yet in a WeChat draft.
func (s *Service) SetLayout(ctx context.Context, actor auth.User, accountID int64, layout string) (int, error) {
	if err := s.ownAccount(ctx, actor, accountID); err != nil {
		return 0, err
	}
	if !render.ValidLayout(layout) {
		return 0, ErrInvalid{"请选择一个排版模板"}
	}
	if _, err := s.DB.Exec(ctx, `UPDATE wx_account SET layout=$2, updated_at=now() WHERE id=$1`, accountID, layout); err != nil {
		return 0, err
	}
	n := 0
	if s.Render != nil {
		var err error
		if n, err = s.Render.Rerender(ctx, accountID); err != nil {
			return n, err
		}
	}
	s.Audit.Log(ctx, actor.ID, "account.layout", "", map[string]any{"account_id": accountID, "layout": layout, "rerendered": n})
	return n, nil
}

func (s *Service) hSetLayout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Layout string `json:"layout"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		server.WriteError(w, 400, "请求格式不正确")
		return
	}
	n, err := s.SetLayout(r.Context(), actor(r), pid(r, "id"), in.Layout)
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]any{"layout": in.Layout, "rerendered": n})
}

// ---------- content calendar ----------

type calArticle struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Time   string `json:"time"`
}

type calPlan struct {
	PlanID int64  `json:"plan_id"`
	Time   string `json:"time"`
	Column string `json:"column"`
}

type calDay struct {
	Date     string       `json:"date"`
	Weekday  int          `json:"weekday"` // 1 = Monday … 7 = Sunday
	Articles []calArticle `json:"articles"`
	Planned  []calPlan    `json:"planned"` // today (not yet run) and later days only
}

var cst = time.FixedZone("CST", 8*3600)

// hCalendar: ?month=YYYY-MM (China time). Articles written that month, and plan slots still to come.
func (s *Service) hCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := pid(r, "id")
	if err := s.ownAccount(ctx, actor(r), id); err != nil {
		fail(w, err)
		return
	}
	now := s.now().In(cst)
	m, err := time.ParseInLocation("2006-01", r.URL.Query().Get("month"), cst)
	if err != nil {
		m = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, cst)
	}
	end := m.AddDate(0, 1, 0)
	days := []*calDay{}
	byDate := map[string]*calDay{}
	for d := m; d.Before(end); d = d.AddDate(0, 0, 1) {
		wd := int(d.Weekday())
		if wd == 0 {
			wd = 7
		}
		x := &calDay{Date: d.Format("2006-01-02"), Weekday: wd, Articles: []calArticle{}, Planned: []calPlan{}}
		days = append(days, x)
		byDate[x.Date] = x
	}
	rows, err := s.DB.Query(ctx, `SELECT id, title, status, created_at FROM article WHERE account_id=$1 AND created_at >= $2 AND created_at < $3 ORDER BY created_at`, id, m, end)
	if err != nil {
		fail(w, err)
		return
	}
	for rows.Next() {
		var a calArticle
		var at time.Time
		if err := rows.Scan(&a.ID, &a.Title, &a.Status, &at); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		at = at.In(cst)
		a.Time = at.Format("15:04")
		if d := byDate[at.Format("2006-01-02")]; d != nil {
			d.Articles = append(d.Articles, a)
		}
	}
	rows.Close()

	var paused bool
	s.DB.QueryRow(ctx, `SELECT paused FROM wx_account WHERE id=$1`, id).Scan(&paused)
	prow, err := s.DB.Query(ctx, `SELECT p.id, p.weekdays, p.at_minute, p.last_run_on, COALESCE(c.name,'自动选栏目')
		FROM write_plan p LEFT JOIN account_column c ON c.id=p.column_id WHERE p.account_id=$1 AND p.enabled ORDER BY p.at_minute`, id)
	if err != nil {
		fail(w, err)
		return
	}
	defer prow.Close()
	today := now.Format("2006-01-02")
	nowMin := now.Hour()*60 + now.Minute()
	for prow.Next() {
		var pl calPlan
		var wds []int16
		var minute int
		var last *time.Time
		if err := prow.Scan(&pl.PlanID, &wds, &minute, &last, &pl.Column); err != nil {
			fail(w, err)
			return
		}
		pl.Time = fmt.Sprintf("%02d:%02d", minute/60, minute%60)
		if paused {
			continue
		}
		for _, d := range days {
			if d.Date < today {
				continue
			}
			if d.Date == today && (minute < nowMin || (last != nil && last.Format("2006-01-02") == today)) {
				continue
			}
			for _, wd := range wds {
				if int(wd) == d.Weekday {
					d.Planned = append(d.Planned, pl)
				}
			}
		}
	}
	server.WriteJSON(w, 200, map[string]any{"month": m.Format("2006-01"), "today": today, "paused": paused, "days": days})
}
