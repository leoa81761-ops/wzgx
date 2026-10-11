package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/config"
	"github.com/leoa81761-ops/wzgx/internal/probe"
	"github.com/leoa81761-ops/wzgx/internal/server"
	_ "time/tzdata"
)

func main() {
	log.SetFlags(0)
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		serve()
	case "probe":
		os.Exit(runProbe())
	case "healthcheck":
		os.Exit(runHealth())
	default:
		fmt.Fprintf(os.Stderr, "用法: gonghao [serve|probe|healthcheck]\n")
		os.Exit(2)
	}
}

func serve() {
	cfg := config.Load()
	app, err := server.Open(cfg)
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
	defer app.Close()
	if cfg.SchedulerEnabled {
		app.StartScheduler()
	}
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("公号智写正在监听 %s（并发上限 %d）", cfg.HTTPAddr, cfg.MaxConcurrency)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务退出: %v", err)
	}
}

func runProbe() int {
	cfg := probe.FromEnv()
	if msg := cfg.Missing(); msg != "" {
		fmt.Fprintf(os.Stderr, "真实连通测试缺少配置：%s\n", msg)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	steps, err := probe.Run(ctx, cfg)
	for _, step := range steps {
		mark := "成功"
		if !step.OK {
			mark = "失败"
		}
		fmt.Printf("%s：%s %s\n", step.Name, mark, step.Detail)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "真实连通测试未完成：%v\n", err)
		return 1
	}
	fmt.Println("真实连通测试完成，草稿已留在公众号草稿箱。")
	return 0
}

func runHealth() int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL(), nil)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "健康检查失败: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok"`) {
		fmt.Fprintf(os.Stderr, "健康检查未通过: %s\n", strings.TrimSpace(string(body)))
		return 1
	}
	return 0
}

func healthURL() string {
	addr := config.Load().HTTPAddr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	addr = strings.Replace(addr, "0.0.0.0", "127.0.0.1", 1)
	return "http://" + addr + "/api/health"
}
