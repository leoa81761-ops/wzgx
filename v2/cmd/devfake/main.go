// Command devfake is a local stand-in for every outside service v2 talks to, for browser walk-throughs:
// the WeChat API (token, image upload, draft box), an OpenAI-compatible streaming model (one path per
// stage: /topic/v1, /research/v1, /write/v1, /review/v1), Tavily, and a cover image at /cover.jpg.
// It listens on loopback only and never forwards anything anywhere. Not for production.
//
// AppSecret behaviour: a secret starting with "bad" fails as a wrong secret (40125), one starting with
// "ip" fails as a missing IP whitelist entry (40164); anything else is accepted.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	jpg "image/jpeg"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// jpeg is a real 900×500 cover (soft green gradient) so previews and uploads look like a picture.
var jpeg = func() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 900, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 900; x++ {
			img.Set(x, y, color.RGBA{uint8(200 - y/10), uint8(230 - x/30), uint8(215 - y/12), 255})
		}
	}
	var b bytes.Buffer
	jpg.Encode(&b, img, &jpg.Options{Quality: 80})
	return b.Bytes()
}()

type fake struct {
	mu     sync.Mutex
	topic  string // last topic handed out; the next write uses it as the title (single-user local tool)
	tokens int
	drafts int
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "loopback listen address")
	flag.Parse()
	host, _, _ := net.SplitHostPort(*addr)
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		log.Fatal("devfake listens on loopback only")
	}
	f := &fake{}
	log.Printf("devfake on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, f))
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/cover.jpg":
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(jpeg)
	case p == "/cgi-bin/stable_token":
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		sec, _ := in["secret"].(string)
		switch {
		case strings.HasPrefix(sec, "bad"):
			fmt.Fprint(w, `{"errcode":40125,"errmsg":"invalid appsecret"}`)
		case strings.HasPrefix(sec, "ip"):
			fmt.Fprint(w, `{"errcode":40164,"errmsg":"invalid ip 127.0.0.1, not in whitelist"}`)
		default:
			f.mu.Lock()
			f.tokens++
			n := f.tokens
			f.mu.Unlock()
			fmt.Fprintf(w, `{"access_token":"FAKETOKEN%d","expires_in":7200}`, n)
		}
	case p == "/cgi-bin/media/uploadimg":
		io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, `{"url":"http://mmbiz.qpic.cn/mmbiz_jpg/devfake/0"}`)
	case p == "/cgi-bin/material/add_material":
		io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, `{"media_id":"FAKETHUMB","url":"http://mmbiz.qpic.cn/devfake/thumb"}`)
	case p == "/cgi-bin/draft/add":
		io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		f.drafts++
		n := f.drafts
		f.mu.Unlock()
		log.Printf("draft/add #%d received (kept in memory only)", n)
		fmt.Fprintf(w, `{"media_id":"FAKEDRAFT%d"}`, n)
	case p == "/search": // Tavily
		io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, `{"results":[{"title":"建筑照明设计标准","url":"https://www.mohurd.gov.cn/devfake","content":"学习桌面照度不低于 300 lx。","published_date":"2026-09-01"}]}`)
	case strings.HasSuffix(p, "/chat/completions"):
		f.model(w, r, strings.Split(strings.TrimPrefix(p, "/"), "/")[0])
	default:
		http.NotFound(w, r)
	}
}

func (f *fake) model(w http.ResponseWriter, r *http.Request, stage string) {
	io.Copy(io.Discard, r.Body)
	// one shared counter used to number titles, so topic/research/write calls of one article skipped
	// numbers and a restart repeated them; label by time instead
	stamp := time.Now().Format("01-02 15:04:05")
	text := ""
	switch stage {
	case "topic":
		// the pipeline refuses a topic it already used for the account, so rotate through a few
		ts := []string{"家里的台灯怎么选才护眼", "冰箱里的剩菜放几天", "老人手机字太小怎么调", "雨天衣服怎么晾干得快", "厨房下水道有味怎么办", "孩子写作业坐姿怎么纠正"}
		pick := ts[rand.IntN(len(ts))]
		t := pick + "（" + stamp + "）"
		f.mu.Lock()
		f.topic = pick
		f.mu.Unlock()
		text = `{"topic":"` + t + `","angle":"从日常使用说起","search_query":"生活 小窍门"}`
	case "research":
		text = "- 学习桌面照度不低于 300 lx（来源：建筑照明设计标准｜GB 50034-2024｜2024-03-01）\n- 台灯放在写字手的对侧，减少阴影（来源：演示资料｜｜2026-09-01）"
	case "write":
		f.mu.Lock()
		title := f.topic
		f.mu.Unlock()
		if title == "" {
			title = "台灯怎么选才护眼"
		}
		text = fmt.Sprintf(`{"title":"%s（演示 %s）","summary":"这是本地演示服务写的假文章，用来走一遍页面。",
"blocks":[{"type":"p","text":"这是一篇本地演示文章，内容是假模型生成的，只用来看页面效果。"},
{"type":"h2","text":"先看亮度"},{"type":"p","text":"学习桌面照度不低于 300 lx[1]。"},
{"type":"h2","text":"再看位置"},{"type":"list","items":["放在写字手的对侧","灯罩不透光","离桌面 40 厘米左右"]},
{"type":"p","text":"选好台灯，孩子写作业也舒服些。"}]}`, title, stamp)
	case "review":
		text = `{"pass":true,"problems":[]}`
	default:
		text = "未检索到可靠来源"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl, _ := w.(http.Flusher)
	rs := []rune(text)
	for i := 0; i < len(rs); i += 60 {
		b, _ := json.Marshal(map[string]any{"model": "devfake-" + stage, "choices": []any{map[string]any{"delta": map[string]string{"content": string(rs[i:min(i+60, len(rs))])}}}})
		fmt.Fprintf(w, "data: %s\n\n", b)
		if fl != nil {
			fl.Flush()
		}
		time.Sleep(20 * time.Millisecond)
	}
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
}
