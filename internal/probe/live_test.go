package probe

import (
	"context"
	"os"
	"testing"
	"time"
)

// 只有同时提供真实公众号和模型凭证时才打外部接口。默认跳过，避免 CI 误连。
func TestLiveDraftWhenCredentialsPresent(t *testing.T) {
	cfg := FromEnv()
	if os.Getenv("WX_APP_ID") == "" || os.Getenv("WX_APP_SECRET") == "" || os.Getenv("LLM_BASE_URL") == "" || os.Getenv("LLM_API_KEY") == "" || os.Getenv("LLM_MODEL") == "" {
		t.Skip("未设置 WX_APP_ID / WX_APP_SECRET / LLM_BASE_URL / LLM_API_KEY / LLM_MODEL，跳过真实草稿连通测试")
	}
	if msg := cfg.Missing(); msg != "" {
		t.Skip(msg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	steps, err := Run(ctx, cfg)
	for _, step := range steps {
		t.Logf("%s ok=%v %s", step.Name, step.OK, step.Detail)
	}
	if err != nil {
		t.Fatal(err)
	}
}
