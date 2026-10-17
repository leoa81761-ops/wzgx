package gen

import "testing"

func TestParseFacts(t *testing.T) {
	text := "根据检索：\n" +
		"- 2026年9月起实施的《数据中心设计规范》GB 50174-2017 要求主机房温度 18–27℃（来源：数据中心设计规范｜GB 50174-2017｜2017-05-04）\n" +
		"- **工信部**印发通知，推进智慧楼宇建设（来源：关于推进智慧楼宇建设的通知｜工信部〔2026〕88号｜2026年9月12日）\n" +
		"2. 某市招标公告显示视频监控改造预算 1200 万元（出处：某市公共资源交易中心 | 2026-10-02）\n" +
		"- 没有来源的一条事实也要保留下来\n"
	fs := ParseFacts(text)
	if len(fs) != 4 {
		t.Fatalf("got %d: %+v", len(fs), fs)
	}
	if fs[0].DocNumber != "GB 50174-2017" || fs[0].Date != "2017-05-04" || fs[0].Title != "数据中心设计规范" {
		t.Fatalf("std: %+v", fs[0])
	}
	if fs[1].DocNumber != "工信部〔2026〕88号" || fs[1].Date != "2026-09-12" {
		t.Fatalf("gov: %+v", fs[1])
	}
	if fs[2].Date != "2026-10-02" || fs[2].Title != "某市公共资源交易中心" {
		t.Fatalf("half-width: %+v", fs[2])
	}
	for i := range fs {
		classify(&fs[i])
	}
	if fs[0].Kind != "national_standard" || fs[1].Kind != "gov_document" || fs[2].Kind != "other" {
		t.Fatalf("kinds %s %s %s", fs[0].Kind, fs[1].Kind, fs[2].Kind)
	}
	bare := ParseFacts("- 工业和信息化部办公厅关于征集智能光伏典型案例的通知｜工信厅联电子函〔2025〕526号｜2025-12-18")
	if len(bare) != 1 || bare[0].DocNumber != "工信厅联电子函〔2025〕526号" || bare[0].Date != "2025-12-18" {
		t.Fatalf("bare bars %+v", bare)
	}
	classify(&bare[0])
	if bare[0].Kind != "gov_document" {
		t.Fatalf("bare kind %s", bare[0].Kind)
	}
	if l := ParseFacts("- 综合布线选型应结合环境干扰场强度｜标准号：GB 50311-2016｜2016-08-26"); len(l) != 1 || l[0].DocNumber != "GB 50311-2016" {
		t.Fatalf("labels %+v", l)
	}
	if ParseFacts("未检索到可靠来源") != nil || ParseFacts("") != nil {
		t.Fatal("nothing found")
	}
	if p := ParseFacts("一段没有条目的检索摘要，说的是近期节能家电补贴政策的情况，内容比较完整。"); len(p) != 1 || p[0].Snippet == "" {
		t.Fatalf("prose line kept as a fact %+v", p)
	}
	if p := ParseFacts("**{\"facts\":[]}**"); len(p) != 0 {
		t.Fatalf("empty JSON answer must be nothing: %+v", p)
	}
	if p := ParseFacts(`{"notes":"- 事实一条，内容足够长（来源：某报｜2026-10-01）"}`); len(p) != 1 || p[0].Date != "2026-10-01" {
		t.Fatalf("notes JSON %+v", p)
	}
}
