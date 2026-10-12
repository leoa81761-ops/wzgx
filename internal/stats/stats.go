package stats

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

// Row 是一天里的一条统计。Key 为空或 follows 表示账号级，其它是微信 msgid。
// Title 有值时才能把这篇阅读对到账号里的文章，没有标题就不猜选题。
type Row struct {
	Date    string
	Key     string
	Title   string
	Reads   int
	Shares  int
	Favors  int
	Follows int
}

// PullDue 在北京时间 8 点之后才拉前一天。同一小时里按账号错开分钟。
func PullDue(now time.Time, accountID int64) bool {
	local := schedule.ShanghaiNow(now)
	if local.Hour() < 8 {
		return false
	}
	if local.Hour() == 8 && local.Minute() < int(accountID%20) {
		return false
	}
	return true
}

func Yesterday(now time.Time) string {
	return schedule.ShanghaiNow(now).AddDate(0, 0, -1).Format("2006-01-02")
}

// Collect 调用发表内容阅读、分享和用户增减接口。三个都失败时返回错误，不把 0 当成真实数据。
func Collect(ctx context.Context, client wechat.Client, day string) ([]Row, error) {
	readRaw, readErr := client.Datacube(ctx, "/datacube/getarticleread", day, day)
	shareRaw, shareErr := client.Datacube(ctx, "/datacube/getarticleshare", day, day)
	userRaw, userErr := client.Datacube(ctx, "/datacube/getusersummary", day, day)
	if readErr != nil && shareErr != nil && userErr != nil {
		return nil, readErr
	}
	byKey := map[string]*Row{}
	if readErr == nil {
		for _, row := range parseArticle(readRaw, "read") {
			item := byKey[row.Key]
			if item == nil {
				copy := row
				byKey[row.Key] = &copy
				continue
			}
			item.Reads += row.Reads
			if item.Title == "" {
				item.Title = row.Title
			}
		}
	}
	if shareErr == nil {
		for _, row := range parseArticle(shareRaw, "share") {
			item := byKey[row.Key]
			if item == nil {
				copy := row
				byKey[row.Key] = &copy
				continue
			}
			item.Shares += row.Shares
			if item.Title == "" {
				item.Title = row.Title
			}
		}
	}
	var out []Row
	for _, item := range byKey {
		if item.Date == "" {
			item.Date = day
		}
		out = append(out, *item)
	}
	if userErr == nil {
		if row, ok := parseFollows(userRaw, day); ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func parseArticle(data map[string]any, kind string) []Row {
	raw, _ := data["list"].([]any)
	var out []Row
	for _, item := range raw {
		row, _ := item.(map[string]any)
		if row == nil {
			continue
		}
		key, _ := row["msgid"].(string)
		if key == "" {
			continue
		}
		detail, _ := row["detail"].(map[string]any)
		if detail == nil {
			continue
		}
		got := Row{Date: text(row["ref_date"]), Key: key, Title: text(row["title"])}
		if kind == "read" {
			got.Reads = number(detail["read_user"])
		} else {
			got.Shares = number(detail["share_user"])
		}
		out = append(out, got)
	}
	return out
}

func parseFollows(data map[string]any, day string) (Row, bool) {
	raw, _ := data["list"].([]any)
	total := 0
	found := false
	for _, item := range raw {
		row, _ := item.(map[string]any)
		if row == nil {
			continue
		}
		if text(row["ref_date"]) != "" && text(row["ref_date"]) != day {
			continue
		}
		total += number(row["new_user"])
		found = true
	}
	if !found {
		return Row{}, false
	}
	return Row{Date: day, Key: "follows", Follows: total}, true
}

func text(v any) string {
	s, _ := v.(string)
	return s
}

func number(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

// Describe 只复述已保存的数字。
func Describe(rows []Row) (summary, advice string, days int) {
	reads, shares, favors, follows := 0, 0, 0, 0
	seen := map[string]bool{}
	for _, row := range rows {
		reads += row.Reads
		shares += row.Shares
		favors += row.Favors
		follows += row.Follows
		if row.Date != "" {
			seen[row.Date] = true
		}
	}
	days = len(seen)
	summary = fmt.Sprintf("阅读 %d，分享 %d，收藏 %d，新增关注 %d。", reads, shares, favors, follows)
	advice = "这些数字来自已保存的统计，没有另外编造。"
	if days < 3 {
		advice += "样本不足，只记录趋势，不调整选题权重。"
	}
	if favors == 0 {
		advice += "发表内容接口没有收藏字段，收藏为 0 时表示尚未手工填写。"
	}
	return summary, advice, days
}

// TopicCount 是某个子方向的反馈次数。
type TopicCount struct {
	Subtopic  string
	Published int
	Discarded int
}

// SuggestWeights 样本少于 3 条时不给权重。0 表示少用，2 表示多用。
func SuggestWeights(rows []TopicCount) (map[string]int, string) {
	out := map[string]int{}
	var notes []string
	for _, row := range rows {
		total := row.Published + row.Discarded
		if row.Subtopic == "" || total < 3 {
			continue
		}
		if row.Discarded >= row.Published+2 {
			out[row.Subtopic] = 0
			notes = append(notes, row.Subtopic+"降低")
		} else if row.Published >= row.Discarded+2 {
			out[row.Subtopic] = 2
			notes = append(notes, row.Subtopic+"提高")
		}
	}
	if len(out) == 0 {
		return nil, "样本不足或没有明显差别，不调整选题权重。"
	}
	return out, "按反馈调整：" + strings.Join(notes, "，") + "。可以在写作计划里撤销。"
}

// ArticleHit is one saved article that certified stats may match.
type ArticleHit struct {
	ID           int64
	Title        string
	Subtopic     string
	DraftMediaID string
}

// Engagement is reads and shares joined to one article. Unmatched msgids are left out.
type Engagement struct {
	Subtopic string
	Title    string
	Reads    int
	Shares   int
}

// MatchReads joins stored stats to articles by title, article id, or draft media id.
// Account-level keys (manual, follows, empty) are not treated as an article.
func MatchReads(articles []ArticleHit, rows []Row) []Engagement {
	type agg struct {
		reads  int
		shares int
		title  string
	}
	byKey := map[string]*agg{}
	for _, row := range rows {
		key := strings.TrimSpace(row.Key)
		if key == "" || key == "empty" || key == "follows" || key == "manual" {
			continue
		}
		item := byKey[key]
		if item == nil {
			item = &agg{title: strings.TrimSpace(row.Title)}
			byKey[key] = item
		}
		item.reads += row.Reads
		item.shares += row.Shares
		if item.title == "" {
			item.title = strings.TrimSpace(row.Title)
		}
	}
	var out []Engagement
	used := map[string]bool{}
	for _, article := range articles {
		title := strings.TrimSpace(article.Title)
		if title == "" || title == "正在生成" || strings.TrimSpace(article.Subtopic) == "" {
			continue
		}
		for key, item := range byKey {
			if used[key] || !sameArticle(article, key, item.title) {
				continue
			}
			used[key] = true
			out = append(out, Engagement{Subtopic: article.Subtopic, Title: title, Reads: item.reads, Shares: item.shares})
			break
		}
	}
	return out
}

func sameArticle(article ArticleHit, key, statTitle string) bool {
	title := strings.TrimSpace(article.Title)
	if statTitle != "" && statTitle == title {
		return true
	}
	if key == title {
		return true
	}
	if key == fmt.Sprint(article.ID) {
		return true
	}
	return article.DraftMediaID != "" && key == article.DraftMediaID
}

// MergeTune keeps feedback weights and fills gaps from certified reads and shares.
// Fewer than three matched articles changes nothing. The returned shape is question or number.
func MergeTune(weights map[string]int, note string, matched []Engagement) (map[string]int, string, string) {
	statWeights, shape, statNote := fromReads(matched)
	if len(statWeights) == 0 && shape == "" {
		return weights, note, ""
	}
	if weights == nil {
		weights = map[string]int{}
	}
	applied := false
	for sub, weight := range statWeights {
		if _, exists := weights[sub]; exists {
			continue
		}
		weights[sub] = weight
		applied = true
	}
	if applied || shape != "" {
		note = strings.TrimRight(note, "。") + "。" + statNote
	}
	if !applied && len(statWeights) > 0 && shape == "" && len(weights) == 0 {
		return nil, note, ""
	}
	return weights, note, shape
}

func fromReads(matched []Engagement) (map[string]int, string, string) {
	if len(matched) < 3 {
		return nil, "", ""
	}
	type bucket struct {
		n     int
		score int
	}
	bySub := map[string]*bucket{}
	for _, item := range matched {
		b := bySub[item.Subtopic]
		if b == nil {
			b = &bucket{}
			bySub[item.Subtopic] = b
		}
		b.n++
		b.score += item.Reads + 3*item.Shares
	}
	type avg struct {
		sub string
		n   int
		avg int
	}
	var qualified []avg
	for sub, b := range bySub {
		if b.n < 3 {
			continue
		}
		qualified = append(qualified, avg{sub: sub, n: b.n, avg: b.score / b.n})
	}
	out := map[string]int{}
	var notes []string
	if len(qualified) >= 2 {
		low, high := qualified[0], qualified[0]
		for _, item := range qualified[1:] {
			if item.avg < low.avg {
				low = item
			}
			if item.avg > high.avg {
				high = item
			}
		}
		if high.sub != low.sub && high.avg >= 3 && high.avg >= low.avg*2 && high.avg > low.avg {
			out[high.sub] = 2
			notes = append(notes, high.sub+"阅读更高")
			if low.avg == 0 {
				out[low.sub] = 0
				notes = append(notes, low.sub+"阅读偏低")
			}
		}
	}
	shape, shapeNote := titleShape(matched)
	if shapeNote != "" {
		notes = append(notes, shapeNote)
	}
	if len(out) == 0 && shape == "" {
		return nil, "", ""
	}
	return out, shape, "按阅读和分享调整：" + strings.Join(notes, "，") + "。"
}

func titleShape(matched []Engagement) (string, string) {
	type bucket struct {
		n     int
		reads int
	}
	groups := map[string]*bucket{}
	for _, item := range matched {
		if item.Reads+item.Shares <= 0 {
			continue
		}
		kind := "plain"
		if strings.ContainsAny(item.Title, "？?") || strings.Contains(item.Title, "为什么") || strings.Contains(item.Title, "怎么") || strings.Contains(item.Title, "吗") {
			kind = "question"
		} else if hasDigit(item.Title) {
			kind = "number"
		}
		b := groups[kind]
		if b == nil {
			b = &bucket{}
			groups[kind] = b
		}
		b.n++
		b.reads += item.Reads
	}
	bestKind := ""
	bestAvg := 0
	restN, restReads := 0, 0
	for kind, b := range groups {
		if kind == "plain" || b.n < 3 {
			restN += b.n
			restReads += b.reads
			continue
		}
		avg := b.reads / b.n
		if avg > bestAvg {
			bestAvg = avg
			bestKind = kind
		}
	}
	if bestKind == "" {
		return "", ""
	}
	for kind, b := range groups {
		if kind == bestKind || kind == "plain" || b.n < 3 {
			continue
		}
		restN += b.n
		restReads += b.reads
	}
	if restN < 1 {
		return "", ""
	}
	restAvg := restReads / restN
	if bestAvg < 2 || bestAvg < restAvg*2 || bestAvg <= restAvg {
		return "", ""
	}
	label := "问句标题"
	if bestKind == "number" {
		label = "带数字的标题"
	}
	return bestKind, label + "阅读更好"
}

func hasDigit(text string) bool {
	for _, r := range text {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}
