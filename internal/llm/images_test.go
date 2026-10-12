package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGenerateCoverURLAndBase64(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a}
	var sawRatio any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/images/generations":
			var body map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			sawRatio = body["aspect_ratio"]
			if body["model"] != "grok-imagine-image-2.0" {
				t.Errorf("model %#v", body["model"])
			}
			if body["response_format"] != "b64_json" {
				t.Errorf("response_format %#v", body["response_format"])
			}
			if r.Header.Get("Authorization") != "Bearer img-key" {
				t.Errorf("auth %s", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"data":[{"url":"` + "http://" + r.Host + `/cover.png"}]}`))
		case "/cover.png":
			_, _ = w.Write(png)
		default:
			var body map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			if body["aspect_ratio"] != "16:9" {
				t.Errorf("ratio %#v", body["aspect_ratio"])
			}
			if body["response_format"] != "b64_json" {
				t.Errorf("response_format %#v", body["response_format"])
			}
			encoded := base64.StdEncoding.EncodeToString(png)
			_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + encoded + `"}]}`))
		}
	}))
	defer srv.Close()
	client := srv.Client()
	got, err := GenerateCover(context.Background(), client, Provider{
		Protocol: "openai", BaseURL: srv.URL + "/v1", APIKey: "img-key", Model: "grok-imagine-image-2.0", Timeout: 5 * time.Second,
	}, "封面")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(png) {
		t.Fatalf("url bytes %x", got)
	}
	if sawRatio != "16:9" {
		t.Fatal(sawRatio)
	}
	got, err = GenerateCover(context.Background(), client, Provider{
		BaseURL: srv.URL + "/b64/v1", APIKey: "img-key", Model: "grok-imagine-image-2.0", Timeout: 5 * time.Second,
	}, "封面")
	if err != nil || string(got) != string(png) {
		t.Fatalf("%v %x", err, got)
	}
}

func TestInterpretResearchIgnoresFreeTextURLs(t *testing.T) {
	notes, sources := InterpretResearch("远程办公的通知见 [人社部](https://www.example.com/notice)。")
	if notes == "" || len(sources) != 0 {
		t.Fatalf("%q %#v", notes, sources)
	}
	notes, sources = InterpretResearch(`{"notes":"只有这一条","sources":[{"name":"甲","title":"乙","url":"https://a.example/x","published_on":"2026-01-02","source_type":"news","note":"说明了办公安排"}]}`)
	if notes != "只有这一条" || len(sources) != 0 {
		t.Fatalf("%q %#v", notes, sources)
	}
	_, sources = InterpretResearch(`{"notes":"见 https://www.example.com/policy","sources":[],"citations":["https://www.gov.cn/zhengce"]}`)
	if len(sources) != 0 {
		t.Fatalf("model JSON citations are not API citations: %#v", sources)
	}
}

func TestStructuredResearchSources(t *testing.T) {
	chat := []byte(`{"choices":[{"message":{"content":"弱电是电子信息系统。参见 https://made-up.invalid/gb"}}],"citations":["https://www.gov.cn/zhengce/2024/elv",{"url":"https://openstd.samr.gov.cn/std/1","title":"标准"}]}`)
	sources := StructuredResearchSources(chat)
	if len(sources) != 2 || sources[0]["url"] != "https://www.gov.cn/zhengce/2024/elv" || sources[1]["title"] != "标准" {
		t.Fatalf("%#v", sources)
	}
	for _, source := range sources {
		if strings.Contains(asString(source["url"]), "made-up") {
			t.Fatal(sources)
		}
	}
	unused := []byte(`{"output_text":"参见 https://www.mohurd.gov.cn/fake","usage":{"num_sources_used":0,"num_server_side_tools_used":0},"output":[{"type":"message","content":[{"type":"output_text","text":"参见 https://www.mohurd.gov.cn/fake","annotations":[{"type":"url_citation","url_citation":{"url":"https://www.mohurd.gov.cn/fake","title":"假的"}}]}]}]}`)
	if got := StructuredResearchSources(unused); len(got) != 0 {
		t.Fatalf("unused tool %#v", got)
	}
	used := []byte(`{"output_text":"说明一下。","usage":{"num_sources_used":1},"output":[{"type":"message","content":[{"type":"output_text","text":"说明一下。","annotations":[{"type":"url_citation","url_citation":{"url":"https://www.miit.gov.cn/a","title":"工信部"}}]}]}]}`)
	sources = StructuredResearchSources(used)
	if len(sources) != 1 || sources[0]["url"] != "https://www.miit.gov.cn/a" || sources[0]["title"] != "工信部" {
		t.Fatalf("%#v", sources)
	}
	search := []byte(`{"choices":[{"message":{"content":"只有说明","annotations":[{"type":"url_citation","url":"https://www.mem.gov.cn/gk/1","title":"应急管理部"}]}}],"search_results":[{"title":"重复","url":"https://www.mem.gov.cn/gk/1"}]}`)
	sources = StructuredResearchSources(search)
	if len(sources) != 1 || sources[0]["url"] != "https://www.mem.gov.cn/gk/1" {
		t.Fatalf("%#v", sources)
	}
	stream := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"参见 https://fake.example/made-up\"}}]}\n\ndata: {\"choices\":[{\"delta\":{}}],\"citations\":[\"https://www.gov.cn/zhengce\"]}\n\ndata: [DONE]\n\n")
	sources = StructuredResearchSources(stream)
	if len(sources) != 1 || sources[0]["url"] != "https://www.gov.cn/zhengce" {
		t.Fatalf("stream %#v", sources)
	}
	var many strings.Builder
	many.WriteString(`{"choices":[{"message":{"content":"检索完成"}}],"citations":[`)
	for i := 0; i < 12; i++ {
		if i > 0 {
			many.WriteString(",")
		}
		fmt.Fprintf(&many, `"https://example.com/p/%d"`, i)
	}
	many.WriteString(`]}`)
	sources = StructuredResearchSources([]byte(many.String()))
	if len(sources) != 12 {
		t.Fatalf("kept %d, want every citation before ranking", len(sources))
	}
}

func TestResearchPromptAsksToSearch(t *testing.T) {
	messages := StageMessages("资料检索", map[string]any{"topic_title": "远程协作"})
	system := messages[0]["content"]
	if !strings.Contains(system, "搜索") || !strings.Contains(system, "中文") || strings.Contains(system, "空数组") || strings.Contains(system, "JSON Schema") {
		t.Fatal(system)
	}
	for _, domain := range []string{"gov.cn", "samr.gov.cn", "std.samr.gov.cn", "openstd.samr.gov.cn", "mohurd.gov.cn"} {
		if !strings.Contains(system, domain) {
			t.Fatalf("prompt missing %s: %s", domain, system)
		}
	}
	elv := StageMessages("资料检索", map[string]any{"vertical_key": "elv", "vertical": "弱电智能化"})
	elvSystem := elv[0]["content"]
	if !strings.Contains(elvSystem, "mohurd.gov.cn") || !strings.Contains(elvSystem, "GB 50311") || !strings.Contains(elvSystem, "samr.gov.cn") {
		t.Fatal(elvSystem)
	}
	short := StageMessages("资料检索", map[string]any{"query_mode": "short"})
	if !strings.Contains(short[0]["content"], "补救检索") {
		t.Fatal(short[0]["content"])
	}
	expand := StageMessages("扩写", map[string]any{"word_target": 800, "expand_goal": 840})
	expandSystem := expand[0]["content"]
	if strings.Contains(expandSystem, "85% 到 130%") || !strings.Contains(expandSystem, "105%") || !strings.Contains(expandSystem, "110%") {
		t.Fatal(expandSystem)
	}
	draft := StageMessages("正文", map[string]any{"word_target": 1500, "avoid_motifs": []any{"温茶转圈"}})
	if !strings.Contains(draft[0]["content"], "word_target") || !strings.Contains(draft[0]["content"], "JSON Schema") || !strings.Contains(draft[0]["content"], "机构名") || !strings.Contains(draft[0]["content"], "温茶转圈") {
		t.Fatal(draft[0]["content"])
	}
	structure := StageMessages("栏目结构", map[string]any{"column_structure": "news"})
	if !strings.Contains(structure[0]["content"], "不要在句首机械地加") || strings.Contains(structure[0]["content"], "答：按上面的问题") {
		t.Fatal(structure[0]["content"])
	}
}
