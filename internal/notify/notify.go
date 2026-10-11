package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// Message 是一次失败或风险提醒。站内通知由调用方写入数据库；Sender 只负责外发。
type Message struct {
	UserID  int64  `json:"user_id"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Account string `json:"account,omitempty"`
}

// Sender 可替换。默认是日志；配置了 NOTIFY_WEBHOOK_URL 时改为 POST JSON。
// 以后接邮件服务时实现同一个接口即可，不要把邮箱服务商密钥写进仓库。
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

type Hook struct {
	URL  string
	HTTP *http.Client
	Log  *log.Logger
}

func (h Hook) Send(ctx context.Context, msg Message) error {
	if h.Log == nil {
		h.Log = log.Default()
	}
	if h.URL == "" {
		h.Log.Printf("通知 %s user=%d %s", msg.Kind, msg.UserID, msg.Title)
		return nil
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, h.URL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := h.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		h.Log.Printf("通知外发失败 user=%d: %v", msg.UserID, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		h.Log.Printf("通知外发状态 %d user=%d", resp.StatusCode, msg.UserID)
	}
	return nil
}
