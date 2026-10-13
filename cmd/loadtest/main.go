package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/llm"
)

func main() {
	serverBin := flag.String("server", "/tmp/gonghao", "gonghao binary")
	cgroup := flag.String("cgroup", "/sys/fs/cgroup/gonghao-load", "cgroup v2 directory already limited to 4 CPU and 4 GiB")
	addr := flag.String("addr", "127.0.0.1:18080", "address the measured server listens on")
	gen := flag.Int("gen", 256, "concurrent generation tasks")
	apiSeconds := flag.Int("api-seconds", 8, "API soak duration")
	llmDelay := flag.Duration("llm-delay", 200*time.Millisecond, "fake model latency per stage")
	wxDelay := flag.Duration("wx-delay", 20*time.Millisecond, "fake WeChat latency per call")
	flag.Parse()

	fake := startFake(*llmDelay, *wxDelay)
	defer fake.Close()
	dbPath := "/tmp/gonghao-load.db"
	_ = os.Remove(dbPath)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")

	cmd := exec.Command(*serverBin, "serve")
	cmd.Env = append(os.Environ(),
		"DATABASE_URL=sqlite://"+dbPath,
		"ADDR="+*addr,
		"LLM_MODE=live",
		"WECHAT_MODE=live",
		"WECHAT_API_BASE="+fake.URL,
		"MAX_CONCURRENCY="+strconv.Itoa(concurrencyLimit(*gen)),
		"DB_MAX_OPEN_CONNS=8",
		"DB_MAX_IDLE_CONNS=4",
		"SCHEDULER_ENABLED=0",
		"MASTER_KEY=load-master-key",
		"JWT_SECRET=load-jwt-secret",
		"ADMIN_EMAIL=admin@example.com",
		"ADMIN_PASSWORD=admin123456",
		"GOMEMLIMIT=3GiB",
		"GOMAXPROCS=4",
	)
	logFile, _ := os.Create("/tmp/gonghao-load.log")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		fatalf("start server: %v", err)
	}
	serverProc = cmd.Process
	defer func() { _ = cmd.Process.Kill() }()
	moveCgroup(*cgroup, cmd.Process.Pid)
	base := "http://" + *addr
	waitHealthy(base)

	client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{MaxIdleConns: 512, MaxIdleConnsPerHost: 512}}
	apiStop := make(chan struct{})
	apiPeakCh := sampleMem(*cgroup, apiStop)
	apiRPS := benchAPI(client, base, time.Duration(*apiSeconds)*time.Second)
	close(apiStop)
	apiPeak := <-apiPeakCh
	admin := login(client, base, "admin@example.com", "admin123456")
	createProvider(client, base, admin, fake.URL+"/v1")

	users := prepareUsers(client, base, *gen)
	genStop := make(chan struct{})
	genPeakCh := sampleMem(*cgroup, genStop)
	before := time.Now()
	cpuStart := cpuUsage(*cgroup)
	ok, fail, sample := burstGenerate(client, base, users)
	elapsed := time.Since(before)
	cpuEnd := cpuUsage(*cgroup)
	close(genStop)
	peak := <-genPeakCh
	current := memCurrent(*cgroup)
	cpuPct := 0.0
	if elapsed > 0 {
		cpuPct = float64(cpuEnd-cpuStart) / float64(elapsed.Microseconds()) / 4 * 100
	}
	fmt.Printf("API_RPS %.1f\n", apiRPS)
	fmt.Printf("API_PEAK_BYTES %d\n", apiPeak)
	fmt.Printf("GEN_OK %d\n", ok)
	fmt.Printf("GEN_FAIL %d\n", fail)
	fmt.Printf("GEN_SECONDS %.2f\n", elapsed.Seconds())
	fmt.Printf("GEN_PEAK_BYTES %d\n", peak)
	fmt.Printf("GEN_CURRENT_BYTES %d\n", current)
	fmt.Printf("GEN_CPU_PERCENT_OF_4 %.1f\n", cpuPct)
	fmt.Printf("GEN_SAMPLE %s\n", sample)
	fmt.Printf("LLM_DELAY %s\n", llmDelay.String())
	fmt.Printf("WX_DELAY %s\n", wxDelay.String())
	fmt.Printf("MAX_CONCURRENCY %d\n", concurrencyLimit(*gen))
	fmt.Printf("CGROUP_MEM_MAX %d\n", readInt(*cgroup+"/memory.max"))
	fmt.Printf("CGROUP_CPU_MAX %s\n", strings.TrimSpace(readText(*cgroup+"/cpu.max")))
	if fail > 0 && ok == 0 {
		os.Exit(1)
	}
}

func concurrencyLimit(gen int) int {
	if gen < 128 {
		return 128
	}
	if gen > 4096 {
		return 4096
	}
	return gen
}

func startFake(llmDelay, wxDelay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stable_token"):
			time.Sleep(wxDelay)
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":7200}`))
		case strings.HasSuffix(r.URL.Path, "/material/add_material"):
			time.Sleep(wxDelay)
			_, _ = w.Write([]byte(`{"media_id":"thumb-load"}`))
		case strings.HasSuffix(r.URL.Path, "/media/uploadimg"):
			time.Sleep(wxDelay)
			_, _ = w.Write([]byte(`{"url":"https://mmbiz.qpic.cn/load"}`))
		case strings.HasSuffix(r.URL.Path, "/draft/add"), strings.HasSuffix(r.URL.Path, "/draft/update"):
			time.Sleep(wxDelay)
			_, _ = w.Write([]byte(`{"media_id":"draft-load"}`))
		case strings.HasSuffix(r.URL.Path, "/draft/get"):
			time.Sleep(wxDelay)
			_, _ = w.Write([]byte(`{"news_item":[{"title":"压测"}]}`))
		case strings.HasSuffix(r.URL.Path, "/draft/count"):
			time.Sleep(wxDelay)
			_, _ = w.Write([]byte(`{"total_count":1}`))
		case strings.HasSuffix(r.URL.Path, "/draft/delete"):
			time.Sleep(wxDelay)
			_, _ = w.Write([]byte(`{"errcode":0}`))
		case strings.Contains(r.URL.Path, "/chat/completions"):
			time.Sleep(llmDelay)
			raw, _ := io.ReadAll(r.Body)
			_, _ = w.Write(llmBody(stageOf(raw)))
		default:
			w.WriteHeader(404)
		}
	}))
}

func llmBody(stage string) []byte {
	var data map[string]any
	switch stage {
	case "选题":
		data = map[string]any{"title": "压测选题"}
	case "大纲":
		data = map[string]any{"points": []any{map[string]any{"heading": "甲", "intent": "说明"}, map[string]any{"heading": "乙", "intent": "说明"}}, "closing": "结束"}
	default:
		data = map[string]any{"title": "压测标题", "summary": "摘要", "sections": []any{map[string]any{"heading": "甲", "paragraphs": []any{"这是压测正文。"}, "list": []any{}}}, "closing": "结束"}
	}
	raw, _ := json.Marshal(data)
	out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(raw)}}}})
	return out
}

func stageOf(raw []byte) string {
	var req map[string]any
	_ = json.Unmarshal(raw, &req)
	msgs, _ := req["messages"].([]any)
	if len(msgs) == 0 {
		return ""
	}
	last, _ := msgs[len(msgs)-1].(map[string]any)
	content, _ := last["content"].(string)
	var payload map[string]any
	_ = json.Unmarshal([]byte(llm.PayloadText(content)), &payload)
	stage, _ := payload["环节"].(string)
	return stage
}

func waitHealthy(base string) {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/api/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	fatalf("server did not become healthy, see /tmp/gonghao-load.log")
}

func sampleMem(dir string, stop <-chan struct{}) <-chan int64 {
	out := make(chan int64, 1)
	go func() {
		var peak int64
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			if n := memCurrent(dir); n > peak {
				peak = n
			}
			select {
			case <-stop:
				if n := memCurrent(dir); n > peak {
					peak = n
				}
				out <- peak
				return
			case <-tick.C:
			}
		}
	}()
	return out
}

func benchAPI(client *http.Client, base string, d time.Duration) float64 {
	var reqs atomic.Int64
	stop := time.Now().Add(d)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(stop) {
				resp, err := client.Get(base + "/api/health")
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					if resp.StatusCode == 200 {
						reqs.Add(1)
					}
				}
				resp, err = client.Get(base + "/api/meta")
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					if resp.StatusCode == 200 {
						reqs.Add(1)
					}
				}
			}
		}()
	}
	wg.Wait()
	return float64(reqs.Load()) / d.Seconds()
}

type userSlot struct {
	token  string
	planID int64
}

func prepareUsers(client *http.Client, base string, n int) []userSlot {
	out := make([]userSlot, n)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	var fail atomic.Int64
	var sample atomic.Value
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			email := fmt.Sprintf("load%d@example.com", i)
			reg := doJSON(client, http.MethodPost, base+"/api/auth/register", "", map[string]any{"account": email, "password": "password12", "agree": true})
			token, _ := reg["token"].(string)
			if token == "" {
				fail.Add(1)
				raw, _ := json.Marshal(reg)
				sample.CompareAndSwap(nil, "register "+email+" "+string(raw))
				return
			}
			bound := doJSON(client, http.MethodPost, base+"/api/accounts", token, map[string]any{
				"name": "压测号", "app_id": fmt.Sprintf("wxload%06d", i), "app_secret": "load-secret",
				"subject_type": "personal", "service_type": "subscription", "verified": false, "consent": true,
			})
			accountID, _ := bound["id"].(float64)
			if accountID == 0 {
				fail.Add(1)
				raw, _ := json.Marshal(bound)
				sample.CompareAndSwap(nil, "bind "+string(raw))
				return
			}
			plan := doJSON(client, http.MethodPost, base+"/api/plans", token, map[string]any{
				"wx_account_id": int64(accountID), "direction": "职场成长", "style_key": "warm", "template_key": "simple",
				"word_target": 800, "schedule_type": "daily", "weekdays": []int{}, "time_window": "night",
			})
			planID, _ := plan["id"].(float64)
			if planID == 0 {
				fail.Add(1)
				raw, _ := json.Marshal(plan)
				sample.CompareAndSwap(nil, "plan "+string(raw))
				return
			}
			out[i] = userSlot{token: token, planID: int64(planID)}
		}(i)
	}
	wg.Wait()
	if fail.Load() > 0 {
		msg, _ := sample.Load().(string)
		fatalf("prepare users failed: %d; sample: %s", fail.Load(), msg)
	}
	return out
}

func burstGenerate(client *http.Client, base string, users []userSlot) (ok, fail int64, sample string) {
	var okN, failN atomic.Int64
	var sampleMu sync.Mutex
	var wg sync.WaitGroup
	genClient := &http.Client{Timeout: 90 * time.Second, Transport: client.Transport}
	for _, user := range users {
		wg.Add(1)
		go func(user userSlot) {
			defer wg.Done()
			body := doJSON(genClient, http.MethodPost, base+"/api/plans/"+strconv.FormatInt(user.planID, 10)+"/generate", user.token, map[string]any{})
			if body["status"] == "generating" {
				id := int64(body["id"].(float64))
				deadline := time.Now().Add(3 * time.Minute)
				for time.Now().Before(deadline) {
					time.Sleep(100 * time.Millisecond)
					polled := doJSON(genClient, http.MethodGet, base+"/api/articles/"+strconv.FormatInt(id, 10), user.token, nil)
					if polled["status"] == "generating" || polled["status"] == nil {
						continue
					}
					body = polled
					break
				}
			}
			if body["status"] == "in_draft" {
				okN.Add(1)
				return
			}
			failN.Add(1)
			sampleMu.Lock()
			if sample == "" {
				raw, _ := json.Marshal(body)
				sample = string(raw)
			}
			sampleMu.Unlock()
		}(user)
	}
	wg.Wait()
	return okN.Load(), failN.Load(), sample
}

func login(client *http.Client, base, account, password string) string {
	body := doJSON(client, http.MethodPost, base+"/api/auth/login", "", map[string]any{"account": account, "password": password})
	token, _ := body["token"].(string)
	return token
}

func createProvider(client *http.Client, base, token, llmBase string) {
	body := doJSON(client, http.MethodPost, base+"/api/admin/providers", token, map[string]any{
		"name": "压测模型", "protocol": "openai", "base_url": llmBase, "api_key": "load-key",
		"model": "load-writer", "extra_headers": map[string]string{}, "extra_params": map[string]any{},
		"priority": 1, "enabled": true, "timeout_seconds": 30,
	})
	if body["id"] == nil {
		fatalf("create provider: %v", body)
	}
}

func doJSON(client *http.Client, method, url, token string, payload any) map[string]any {
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequest(method, url, bytes.NewReader(raw))
	if err != nil {
		return map[string]any{"detail": err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return map[string]any{"detail": err.Error()}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var body map[string]any
	if json.Unmarshal(data, &body) != nil {
		return map[string]any{"detail": string(data), "status_code": resp.StatusCode}
	}
	body["status_code"] = resp.StatusCode
	return body
}

func moveCgroup(dir string, pid int) {
	if dir == "" {
		return
	}
	cmd := exec.Command("sudo", "-n", "tee", dir+"/cgroup.procs")
	cmd.Stdin = strings.NewReader(strconv.Itoa(pid) + "\n")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		fatalf("move pid %d into cgroup: %v", pid, err)
	}
}

func memCurrent(dir string) int64 { return readInt(dir + "/memory.current") }

func cpuUsage(dir string) int64 {
	raw, err := os.ReadFile(dir + "/cpu.stat")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "usage_usec ") {
			n, _ := strconv.ParseInt(strings.TrimPrefix(line, "usage_usec "), 10, 64)
			return n
		}
	}
	return 0
}

func readText(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func readInt(path string) int64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	return n
}

var serverProc *os.Process

func fatalf(format string, args ...any) {
	if serverProc != nil {
		_ = serverProc.Kill()
	}
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
