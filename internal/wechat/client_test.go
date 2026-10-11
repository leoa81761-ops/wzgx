package wechat

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestMockAddAndUpdate(t *testing.T) {
	ResetMock()
	client := newMock("wx_mock_1", "secret", "https://api.weixin.qq.com")
	report := CheckPermissions(context.Background(), client, []byte("png"))
	if !report.CanDraft || report.DeliveryMode != "draft" {
		t.Fatalf("%#v", report)
	}
	box := mockStore["wx_mock_1"]
	if len(box.drafts) != 0 {
		t.Fatal("probe draft should be deleted")
	}
	thumb, err := client.UploadThumb(context.Background(), []byte("png"), "cover.png")
	if err != nil {
		t.Fatal(err)
	}
	mediaID, err := client.AddDraft(context.Background(), DraftArticle{Title: "标题", Content: "<p>正文</p>", ThumbMediaID: thumb, Digest: "摘要"})
	if err != nil || !strings.HasPrefix(mediaID, "mock_draft_") {
		t.Fatal(mediaID, err)
	}
	if _, err := client.UpdateDraft(context.Background(), mediaID, DraftArticle{Title: "新标题", Content: "<p>新</p>", ThumbMediaID: thumb, Digest: "摘要"}); err != nil {
		t.Fatal(err)
	}
	saved, err := client.GetDraft(context.Background(), mediaID)
	if err != nil || saved["title"] != "新标题" {
		t.Fatal(saved, err)
	}
}

func TestLiveRequestShapes(t *testing.T) {
	var mu sync.Mutex
	var tokenCalls int
	var addBody map[string]any
	var updateBody map[string]any
	var thumbQuery, imageQuery string
	var thumbName, imageName, thumbType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/stable_token":
			mu.Lock()
			tokenCalls++
			mu.Unlock()
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["grant_type"] != "client_credential" || body["appid"] != "wx123" || body["secret"] != "sec" {
				t.Errorf("token body %#v", body)
			}
			_, _ = w.Write([]byte(`{"access_token":"token-1","expires_in":7200}`))
		case "/cgi-bin/material/add_material":
			thumbQuery = r.URL.RawQuery
			thumbName, thumbType = multipartName(t, r)
			_, _ = w.Write([]byte(`{"media_id":"thumb-1"}`))
		case "/cgi-bin/media/uploadimg":
			imageQuery = r.URL.RawQuery
			imageName, _ = multipartName(t, r)
			_, _ = w.Write([]byte(`{"url":"https://mmbiz.qpic.cn/demo"}`))
		case "/cgi-bin/draft/add":
			_ = json.NewDecoder(r.Body).Decode(&addBody)
			_, _ = w.Write([]byte(`{"media_id":"draft-1"}`))
		case "/cgi-bin/draft/update":
			_ = json.NewDecoder(r.Body).Decode(&updateBody)
			_, _ = w.Write([]byte(`{"errcode":0}`))
		case "/cgi-bin/draft/get":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["media_id"] != "draft-1" {
				t.Errorf("get %#v", body)
			}
			_, _ = w.Write([]byte(`{"news_item":[{"title":"你好"}]}`))
		case "/cgi-bin/draft/count":
			_, _ = w.Write([]byte(`{"total_count":2}`))
		case "/cgi-bin/draft/delete":
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"errcode":404,"errmsg":"missing"}`))
		}
	}))
	defer srv.Close()
	client := NewLive("wx123", "sec", srv.URL, srv.Client(), NewTokenCache())
	if token, err := client.GetToken(context.Background()); err != nil || token != "token-1" {
		t.Fatal(token, err)
	}
	if _, err := client.GetToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tokenCalls != 1 {
		t.Fatalf("token should be cached, calls=%d", tokenCalls)
	}
	thumb, err := client.UploadThumb(context.Background(), []byte("png"), "cover.png")
	if err != nil || thumb != "thumb-1" {
		t.Fatal(thumb, err)
	}
	if !strings.Contains(thumbQuery, "access_token=token-1") || !strings.Contains(thumbQuery, "type=image") || thumbName != "cover.png" || thumbType != "image/png" {
		t.Fatalf("thumb query %s name %s type %s", thumbQuery, thumbName, thumbType)
	}
	imageURL, err := client.UploadContentImage(context.Background(), []byte("png"), "inline.png")
	if err != nil || imageURL != "https://mmbiz.qpic.cn/demo" {
		t.Fatal(imageURL, err)
	}
	if strings.Contains(imageQuery, "type=") || imageName != "inline.png" || !strings.Contains(imageQuery, "access_token=token-1") {
		t.Fatalf("uploadimg query %s name %s", imageQuery, imageName)
	}
	mediaID, err := client.AddDraft(context.Background(), DraftArticle{Title: "你好", Content: "<p>a</p>", ThumbMediaID: "thumb-1", Digest: "d"})
	if err != nil || mediaID != "draft-1" {
		t.Fatal(mediaID, err)
	}
	articles, ok := addBody["articles"].([]any)
	if !ok || len(articles) != 1 {
		t.Fatalf("articles should be an array: %#v", addBody["articles"])
	}
	first := articles[0].(map[string]any)
	if first["title"] != "你好" || first["thumb_media_id"] != "thumb-1" || first["author"] != "公号智写" {
		t.Fatal(first)
	}
	if _, err := client.UpdateDraft(context.Background(), mediaID, DraftArticle{Title: "改", Content: "<p>b</p>", ThumbMediaID: "thumb-1"}); err != nil {
		t.Fatal(err)
	}
	if _, isArr := updateBody["articles"].([]any); isArr {
		t.Fatalf("update articles should be an object: %#v", updateBody["articles"])
	}
	obj, _ := updateBody["articles"].(map[string]any)
	if updateBody["media_id"] != "draft-1" || updateBody["index"] != float64(0) || obj["title"] != "改" {
		t.Fatalf("%#v", updateBody)
	}
	got, err := client.GetDraft(context.Background(), mediaID)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := got["news_item"].([]any)
	if len(items) != 1 {
		t.Fatal(got)
	}
	n, err := client.DraftCount(context.Background())
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if tokenCalls != 1 {
		t.Fatalf("later calls should reuse token, calls=%d", tokenCalls)
	}
}

func TestTokenRefreshOnce(t *testing.T) {
	var calls []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/stable_token" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			force, _ := body["force_refresh"].(bool)
			calls = append(calls, force)
			_, _ = w.Write([]byte(`{"access_token":"token-next","expires_in":7200}`))
			return
		}
		if len(calls) < 2 {
			_, _ = w.Write([]byte(`{"errcode":40001,"errmsg":"invalid credential"}`))
			return
		}
		_, _ = w.Write([]byte(`{"total_count":1}`))
	}))
	defer srv.Close()
	client := NewLive("wx123", "sec", srv.URL, srv.Client(), NewTokenCache())
	n, err := client.DraftCount(context.Background())
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if len(calls) != 2 || calls[0] || !calls[1] {
		t.Fatalf("force_refresh sequence %#v", calls)
	}
}

func TestErrcodeMessagesAndPermission(t *testing.T) {
	if !strings.Contains(Explain(40164, "invalid ip"), "白名单") {
		t.Fatal(Explain(40164, "invalid ip"))
	}
	if !strings.Contains(Explain(45009, "reach max api"), "上限") {
		t.Fatal(Explain(45009, ""))
	}
	if !strings.Contains(Explain(48001, "api unauthorized"), "没有此接口权限") {
		t.Fatal(Explain(48001, ""))
	}
	perm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/stable_token") {
			_, _ = w.Write([]byte(`{"access_token":"token-1","expires_in":7200}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/draft/count") {
			_, _ = w.Write([]byte(`{"errcode":48001,"errmsg":"api unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer perm.Close()
	client := NewLive("wx123", "sec", perm.URL, perm.Client(), NewTokenCache())
	report := CheckPermissions(context.Background(), client, []byte("png"))
	if report.AuthFailed || report.CanDraft || report.DeliveryMode != "export" {
		t.Fatalf("%#v", report)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":40001,"errmsg":"invalid credential"}`))
	}))
	defer bad.Close()
	badClient := NewLive("wx123", "bad", bad.URL, bad.Client(), NewTokenCache())
	if _, err := badClient.GetToken(context.Background()); err == nil || !strings.Contains(err.Error(), "凭证无效") {
		t.Fatal(err)
	}
	report = CheckPermissions(context.Background(), badClient, []byte("png"))
	if !report.AuthFailed {
		t.Fatal("40001 should be auth failed")
	}
	ip := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":40164,"errmsg":"invalid ip"}`))
	}))
	defer ip.Close()
	ipClient := NewLive("wx123", "sec", ip.URL, ip.Client(), NewTokenCache())
	report = CheckPermissions(context.Background(), ipClient, []byte("png"))
	if report.AuthFailed || report.OK {
		t.Fatalf("40164 should fail closed without marking auth invalid: %#v", report)
	}
	if !strings.Contains(report.Checks[0]["detail"].(string), "白名单") {
		t.Fatal(report.Checks[0]["detail"])
	}
}

func TestImageContentTypeFollowsFilename(t *testing.T) {
	if imageContentType("cover.jpg") != "image/jpeg" || imageContentType("cover.jpeg") != "image/jpeg" || imageContentType("cover.png") != "image/png" {
		t.Fatal(imageContentType("cover.jpg"), imageContentType("cover.png"))
	}
}

func multipartName(t *testing.T, r *http.Request) (string, string) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatal(r.Header.Get("Content-Type"), err)
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	part, err := reader.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	defer part.Close()
	if part.FormName() != "media" {
		t.Fatal(part.FormName())
	}
	_, _ = io.ReadAll(part)
	return part.FileName(), part.Header.Get("Content-Type")
}
