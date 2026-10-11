package notify

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"mime"
	"net/smtp"
	"strings"
)

// SMTP 在配置了 SMTP_HOST 且账号是邮箱时发信。没配主机，或账号不是邮箱，就什么都不做。
type SMTP struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	Log      *log.Logger
}

func (s SMTP) Send(ctx context.Context, msg Message) error {
	if s.Log == nil {
		s.Log = log.Default()
	}
	if strings.TrimSpace(s.Host) == "" || !strings.Contains(msg.Account, "@") {
		return nil
	}
	from := strings.TrimSpace(s.From)
	if from == "" {
		from = strings.TrimSpace(s.User)
	}
	if !strings.Contains(from, "@") {
		return nil
	}
	port := s.Port
	if port <= 0 {
		port = 587
	}
	var body bytes.Buffer
	fmt.Fprintf(&body, "From: %s\r\n", from)
	fmt.Fprintf(&body, "To: %s\r\n", msg.Account)
	fmt.Fprintf(&body, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", msg.Title))
	body.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n")
	body.WriteString(msg.Body)
	body.WriteString("\r\n")
	var auth smtp.Auth
	if s.User != "" {
		auth = smtp.PlainAuth("", s.User, s.Password, s.Host)
	}
	err := smtp.SendMail(fmt.Sprintf("%s:%d", s.Host, port), auth, from, []string{msg.Account}, body.Bytes())
	if err != nil {
		s.Log.Printf("邮件通知失败 user=%d: %v", msg.UserID, err)
	}
	return err
}

// Fanout 依次调用多个外发实现。某一个失败不会挡住后面的。
type Fanout struct {
	Senders []Sender
}

func (f Fanout) Send(ctx context.Context, msg Message) error {
	var first error
	for _, sender := range f.Senders {
		if sender == nil {
			continue
		}
		if err := sender.Send(ctx, msg); err != nil && first == nil {
			first = err
		}
	}
	return first
}
