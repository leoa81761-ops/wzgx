package pipeline

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/editorial"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/quality"
)

// The fixture follows the pipeline shape from #28 and #29, when articles 22 and 31
// were the ones that scored well: a sourced steps draft, a real table, a list that
// starts at 1, citations, and about 1500 Chinese characters. Later gates may repair
// a draft. They must not delete that structure.
func TestGoldenLaterGatesKeepTheGoodDraft(t *testing.T) {
	draft := goldenSourcedDraft()
	before := content.BodyRunes(draft)
	if before < 1400 {
		t.Fatalf("fixture %d", before)
	}
	tables, cells := markupCounts(draft)
	lists := listCount(draft)
	out := polishDraft(cloneDraft(draft), Input{})
	out = quality.AlignStructure("steps", out)
	out = editorial.ShapeCopy(out)
	out = editorial.StripFactNumbers(out)
	after := content.BodyRunes(out)
	if after*10 < before*9 {
		t.Fatalf("later gates stripped the draft from %d to %d", before, after)
	}
	gotTables, gotCells := markupCounts(out)
	if gotTables < tables || gotCells < cells {
		t.Fatalf("table %d/%d -> %d/%d", tables, cells, gotTables, gotCells)
	}
	if listCount(out) != lists {
		t.Fatalf("list %d -> %d", lists, listCount(out))
	}
	if !leadingNumbersContiguous(out) {
		t.Fatal("numbering")
	}
	first := ""
	if items := asList(asList(out["sections"])[0].(map[string]any)["list"]); len(items) > 0 {
		first = str(items[0])
	}
	if !strings.HasPrefix(strings.TrimSpace(first), "1.") && !strings.HasPrefix(strings.TrimSpace(first), "1、") {
		t.Fatal(first)
	}
	if !strings.Contains(bodyPlain(out), "[1]") {
		t.Fatal(bodyPlain(out))
	}
}

func TestStructureRewriteKeepsThePreStructureDraft(t *testing.T) {
	draft := goldenSourcedDraft()
	if !structureRewriteOK(draft, draft) {
		t.Fatal("a good draft must accept itself")
	}
	flat := cloneDraft(draft)
	section := asList(flat["sections"])[0].(map[string]any)
	delete(section, "table")
	section["paragraphs"] = []any{"2. 核对项依据层高层高不低于3米。", "3. 下一条。", "8. 中间漏了。"}
	section["list"] = []any{"2. 少了一条。"}
	if structureRewriteOK(draft, flat) {
		t.Fatal("a flattened rewrite must be rejected")
	}
	short := cloneDraft(draft)
	short["sections"] = []any{map[string]any{"paragraphs": []any{"1. 只剩一句。"}, "list": []any{}}}
	short["closing"] = "结束。"
	if structureRewriteOK(draft, short) {
		t.Fatal("a rewrite that loses more than 10% must be rejected")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		writeChoice(w, map[string]any{
			"title": "住宅综合布线验收核对",
			"sections": []any{map[string]any{
				"paragraphs": []any{"2. 表格被写成了一句话。", "8. 序号断了。"},
				"list":       []any{},
			}},
			"closing": "结束",
			"sources": []any{},
		})
	}))
	defer srv.Close()
	gw := llm.NewGateway(false, srv.Client())
	providers := map[string][]llm.Provider{"draft": {{
		Name: "写作", Protocol: "openai", BaseURL: srv.URL + "/v1", Model: "writer", Timeout: 10 * time.Second,
	}}}
	pending := cloneDraft(draft)
	pendingSection := asList(pending["sections"])[0].(map[string]any)
	pendingSection["paragraphs"] = append(asList(pendingSection["paragraphs"]), "这一段还没有序号，表格和列表都还在。")
	kept, _, _, held := rewriteColumnStructure(t.Context(), gw, providers, pending, map[string]any{}, "steps", "")
	if held {
		t.Fatal("rejecting a bad rewrite must keep the pre-structure draft for the later structure check")
	}
	gotTables, _ := markupCounts(kept)
	if gotTables < 1 || listCount(kept) < 3 || !strings.Contains(bodyPlain(kept), "[1]") {
		t.Fatal(bodyPlain(kept), gotTables, listCount(kept))
	}
}

func TestRoadStandardDoesNotCountForResidentialCabling(t *testing.T) {
	title := "住宅综合布线"
	sources := []map[string]any{
		{
			"title": "城市道路交通工程项目规范", "snippet": "城市道路的交通工程应按本规范执行",
			"url":          "https://www.mohurd.gov.cn/gongkai/zc/gz/202201/road.html",
			"fetched_text": "城市道路的交通工程应按本规范执行，标准号是 GB 55011。",
		},
		{
			"title": "国家标准《住宅项目规范》", "snippet": "住宅建筑的层高不应低于3米",
			"url":          "https://www.mohurd.gov.cn/gongkai/zc/gz/202205/home.html",
			"fetched_text": "住宅建筑的层高不应低于3米，这条写在住宅项目规范里。",
		},
		{
			"title": "公共建筑节能设计标准", "snippet": "公共建筑的节能设计按本标准",
			"url":          "https://www.mohurd.gov.cn/gongkai/zc/gz/202203/energy.html",
			"fetched_text": "公共建筑的节能设计按本标准执行并计算能耗。",
		},
	}
	items := []any{
		map[string]any{"index": 1, "score": 3, "page_type": "standard", "on_topic": true, "exact_topic": true, "snippet": "城市道路的交通工程应按本规范执行"},
		map[string]any{"index": 2, "score": 3, "page_type": "official", "on_topic": true, "snippet": "住宅建筑的层高不应低于3米"},
		map[string]any{"index": 3, "score": 3, "page_type": "standard", "on_topic": true, "snippet": "公共建筑的节能设计按本标准"},
	}
	kept, dropped := applyJudgeScores(sources, items, title)
	if len(kept) != 1 || str(kept[0]["title"]) != "国家标准《住宅项目规范》" {
		t.Fatal(kept, dropped)
	}
	reasons := map[string]string{}
	for _, row := range dropped {
		reasons[str(row["title"])] = str(row["reason"])
	}
	if reasons["城市道路交通工程项目规范"] != "和标题对不上" || reasons["公共建筑节能设计标准"] != "和标题对不上" {
		t.Fatal(dropped)
	}
	same := []map[string]any{{
		"title": "住宅综合布线设计说明", "snippet": "住宅综合布线先核对信息点",
		"url": "https://www.mohurd.gov.cn/a", "fetched_text": "住宅综合布线先核对信息点的位置和数量。",
	}}
	kept, dropped = applyJudgeScores(same, []any{
		map[string]any{"index": 1, "score": 3, "page_type": "professional", "on_topic": true, "exact_topic": false, "snippet": "住宅综合布线先核对信息点"},
	}, title)
	if len(kept) != 1 || str(kept[0]["title"]) != "住宅综合布线设计说明" {
		t.Fatal(kept, dropped)
	}
}

func TestTipColumnHoldsAGenericExperience(t *testing.T) {
	generic := map[string]any{"sections": []any{map[string]any{"paragraphs": []any{
		"把杯子放在窗边，人会更从容。",
		"省力的动作让日常更轻松。",
		"在桌前走动一下就好。",
	}}}}
	if !thinExperience(generic, true, "生活小窍门") {
		t.Fatal("a tip column needs five concrete tips")
	}
	if !thinExperience(generic, true, "其他栏目") {
		t.Fatal("generic experience should be held")
	}
	sourced := cloneDraft(generic)
	if thinExperience(sourced, false, "生活小窍门") {
		t.Fatal("a sourced article is not held for being thin")
	}
	tips := map[string]any{"sections": []any{map[string]any{
		"paragraphs": []any{
			"把白醋倒进杯里泡5分钟。",
			"用牙刷蘸小苏打擦水龙头。",
			"晾10分钟再拧干抹布。",
		},
		"list": []any{"贴一条胶带把线槽固定住。", "先量一下窗框宽度再剪。"},
	}}}
	if editorial.ConcreteTips(tips) < 5 || thinExperience(tips, true, "生活小窍门") {
		t.Fatal(editorial.ConcreteTips(tips))
	}
}

func goldenSourcedDraft() map[string]any {
	paragraphs := []any{
		"1. 进户先打开信息箱，把住宅布线的图纸摊开，对照今天的点位。[1]",
		"2. 户内箱挂在入户侧，布线的进线方向要和门轴错开，避免以后改门。",
		"3. 过线盒留在吊顶检修口旁边，布线转弯处不要挤成死角。",
		"4. 面板高度按开关那一排来，布线出线口先封盖再粉刷。",
		"5. 网线单独走一侧，布线和电源保持距离，交叉时垂直过去。[1]",
		"6. 电话和电视不要并在同一根管里，布线分管后再做标签。",
		"7. 门禁线从弱电井引来，布线在井里盘一圈余量再往下。",
		"8. 对讲主机旁留检修空间，布线的接头朝下，防止进水。",
	}
	paragraphs = append(paragraphs, "9. 布线收尾时把余量写进记录。"+strings.Repeat("甲乙丙丁戊己庚辛壬癸", 120))
	return map[string]any{
		"title":   "住宅综合布线验收核对",
		"summary": "这篇只核对进户到户内这一段。",
		"sections": []any{map[string]any{
			"heading":    "进户到户内",
			"paragraphs": paragraphs,
			"list": []any{
				"1. 备好标签机和扎带。",
				"2. 留出可以打开的检修口。",
				"3. 拍下配线架的全貌。",
			},
			"table": map[string]any{
				"headers": []any{"核对项", "现场要求"},
				"rows": []any{
					[]any{"层高", "不低于三米"},
					[]any{"弯曲", "大于四倍外径"},
					[]any{"盘绕", "不少于两圈"},
				},
			},
		}},
		"closing": "关注本号，把你门口的信息箱位置写在评论里。",
		"sources": []any{map[string]any{"title": "住宅项目规范", "url": "https://www.mohurd.gov.cn/home"}},
	}
}
