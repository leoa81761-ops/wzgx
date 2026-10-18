package publish

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"gonghao/internal/auth"
	"gonghao/internal/server"
	"gonghao/internal/wechat"
)

// Article analytics for verified accounts: once a day (after 09:00 Beijing time, when WeChat has
// the previous day's numbers) read datacube/getarticletotaldetail for the days our pushed
// articles were published, match them to our articles and keep the cumulative numbers per day.
// Read only; WeChat keeps each article's numbers for 30 days after publishing.

const (
	statsHour     = 9
	statsLookback = 37 // days after a push we still look for the article
	statsSearch   = 7  // days after a push we search for its publish date while it is unmatched
	statsMaxDays  = 31 // datacube calls per account per pull
)

// StatsDue pulls the day's stats for verified accounts that have not had them yet; worker loop.
func (s *Service) StatsDue(ctx context.Context) error {
	now := s.now().In(china)
	if now.Hour() < statsHour {
		return nil
	}
	today := now.Format("2006-01-02")
	rows, err := s.DB.Query(ctx, `SELECT a.id FROM wx_account a WHERE a.verified AND a.bind_status='bound'
		AND (a.stats_fetched_on IS NULL OR a.stats_fetched_on < $1::date)
		AND EXISTS (SELECT 1 FROM article r WHERE r.account_id=a.id AND r.status='pushed' AND r.pushed_at > $2)
		ORDER BY a.id LIMIT 5`, today, now.AddDate(0, 0, -statsLookback))
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		s.pullStats(ctx, id, now)
	}
	return nil
}

// RefreshStats (user, own verified account): pull now; at most every 10 minutes.
func (s *Service) RefreshStats(ctx context.Context, actor auth.User, accountID int64) (string, error) {
	a, err := s.loadAccount(ctx, actor, accountID)
	if err != nil {
		return "", err
	}
	var verified bool
	var at *time.Time
	s.DB.QueryRow(ctx, `SELECT verified, stats_at FROM wx_account WHERE id=$1`, a.id).Scan(&verified, &at)
	if !verified {
		return "", fmt.Errorf("%w：只有认证的公众号能读取文章数据", ErrState)
	}
	if at != nil && s.now().Sub(*at) < 10*time.Minute {
		return "", fmt.Errorf("%w：刚刚更新过，10 分钟后再试", ErrState)
	}
	return s.pullStats(ctx, a.id, s.now().In(china)), nil
}

type statArticle struct {
	id      int64
	title   string
	url     string
	msgid   string
	refDate string
	pushed  time.Time
}

// pullStats reads and stores; returns the note it saved on the account.
func (s *Service) pullStats(ctx context.Context, accountID int64, now time.Time) string {
	note := s.pullStatsNote(ctx, accountID, now)
	s.DB.Exec(ctx, `UPDATE wx_account SET stats_fetched_on=$2::date, stats_at=$3, stats_note=$4 WHERE id=$1`,
		accountID, now.Format("2006-01-02"), s.now(), note)
	return note
}

func (s *Service) pullStatsNote(ctx context.Context, accountID int64, now time.Time) string {
	var a account
	if err := s.DB.QueryRow(ctx, `SELECT id, user_id, app_id, name, secret_cipher, bind_status FROM wx_account WHERE id=$1`, accountID).
		Scan(&a.id, &a.userID, &a.appID, &a.name, &a.secretCipher, &a.bindStatus); err != nil {
		return "公众号读取失败"
	}
	rows, err := s.DB.Query(ctx, `SELECT id, title, publish_url, wx_msgid, COALESCE(to_char(wx_ref_date,'YYYY-MM-DD'),''), pushed_at
		FROM article WHERE account_id=$1 AND status='pushed' AND pushed_at > $2 ORDER BY pushed_at`, accountID, now.AddDate(0, 0, -statsLookback))
	if err != nil {
		return "读取文章失败"
	}
	var arts []*statArticle
	for rows.Next() {
		x := &statArticle{}
		rows.Scan(&x.id, &x.title, &x.url, &x.msgid, &x.refDate, &x.pushed)
		arts = append(arts, x)
	}
	rows.Close()

	// the publish dates to ask about: known ones, else the week after each push
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	oldest := now.AddDate(0, 0, -30).Format("2006-01-02")
	want := map[string]bool{}
	for _, x := range arts {
		if x.refDate != "" {
			if x.refDate >= oldest {
				want[x.refDate] = true
			}
			continue
		}
		for d := 0; d < statsSearch; d++ {
			day := x.pushed.In(china).AddDate(0, 0, d).Format("2006-01-02")
			if day > yesterday {
				break
			}
			if day >= oldest {
				want[day] = true
			}
		}
	}
	days := make([]string, 0, len(want))
	for d := range want {
		days = append(days, d)
	}
	sort.Strings(days)
	if len(days) > statsMaxDays {
		days = days[len(days)-statsMaxDays:]
	}
	if len(days) == 0 {
		return "最近没有可统计的已发表文章"
	}
	tok, err := s.token(ctx, a, false)
	if err != nil {
		return "获取 access_token 失败：" + errText(err)
	}
	matched := map[int64]bool{}
	for _, day := range days {
		list, err := s.WX.ArticleTotalDetail(ctx, tok, day)
		if err != nil {
			return fmt.Sprintf("读取 %s 的数据失败：%s", day, errText(err))
		}
		for _, st := range list {
			x := matchStat(arts, st)
			if x == nil {
				continue
			}
			x.msgid, x.refDate = st.MsgID, st.RefDate
			s.DB.Exec(ctx, `UPDATE article SET wx_msgid=$2, wx_ref_date=$3::date WHERE id=$1`, x.id, st.MsgID, st.RefDate)
			for _, d := range st.Days {
				if d.Date == "" {
					continue
				}
				s.DB.Exec(ctx, `INSERT INTO article_stat_day (article_id, stat_date, read_user, share_user, collection_user, like_user, zaikan_user, finish_rate)
					VALUES ($1,$2::date,$3,$4,$5,$6,$7,$8) ON CONFLICT (article_id, stat_date) DO UPDATE SET read_user=$3, share_user=$4,
					collection_user=$5, like_user=$6, zaikan_user=$7, finish_rate=$8`,
					x.id, d.Date, d.ReadUser, d.ShareUser, d.CollectionUser, d.LikeUser, d.Zaikan, d.FinishRate)
			}
			matched[x.id] = true
		}
	}
	return fmt.Sprintf("已更新 %d 篇文章的数据", len(matched))
}

// matchStat finds our article for a datacube entry: same msgid, same URL, or (not matched yet)
// the same title pushed no later than the publish date.
func matchStat(arts []*statArticle, st wechat.ArticleStat) *statArticle {
	for _, x := range arts {
		if x.msgid != "" && x.msgid == st.MsgID {
			return x
		}
	}
	u := normURL(st.URL)
	for _, x := range arts {
		if x.msgid == "" && u != "" && normURL(x.url) == u {
			return x
		}
	}
	t := strings.TrimSpace(st.Title)
	for _, x := range arts {
		if x.msgid == "" && t != "" && strings.TrimSpace(x.title) == t && x.pushed.In(china).Format("2006-01-02") <= st.RefDate {
			return x
		}
	}
	return nil
}

func normURL(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.IndexAny(u, "#"); i >= 0 {
		u = u[:i]
	}
	return u
}

func errText(err error) string {
	var ae *wechat.APIError
	if errors.As(err, &ae) {
		if h := ae.Hint(); h != "" {
			return h
		}
	}
	return err.Error()
}

func (s *Service) hRefreshStats(w http.ResponseWriter, r *http.Request) {
	note, err := s.RefreshStats(r.Context(), who(r), id(r))
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]string{"note": note})
}
