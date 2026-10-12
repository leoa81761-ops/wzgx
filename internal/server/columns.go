package server

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/columns"
	"github.com/leoa81761-ops/wzgx/internal/model"
)

func (a *App) listColumns(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "account_id", "找不到这个公众号")
	if err != nil {
		return err
	}
	if _, err := a.ownAccount(r.Context(), nil, user.ID, id); err != nil {
		return err
	}
	rows, err := a.Store.ListAccountColumns(r.Context(), nil, id, "")
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}

type columnBody struct {
	Name        string `json:"name"`
	Positioning string `json:"positioning"`
	Structure   string `json:"structure"`
	WordTarget  int    `json:"word_target"`
	ImageStyle  string `json:"image_style"`
	Weight      int    `json:"weight"`
	Status      string `json:"status"`
}

func (a *App) createColumn(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "account_id", "找不到这个公众号")
	if err != nil {
		return err
	}
	if _, err := a.ownAccount(r.Context(), nil, user.ID, id); err != nil {
		return err
	}
	var body columnBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	col, err := normalizeColumn(body, id)
	if err != nil {
		return err
	}
	if body.Status == "suggested" {
		col.Status = "suggested"
	}
	now := time.Now().UTC()
	if err := a.Store.InsertAccountColumn(r.Context(), nil, &col, now); err != nil {
		return err
	}
	writeJSON(w, 200, col)
	return nil
}

func (a *App) updateColumn(w http.ResponseWriter, r *http.Request, user *model.User) error {
	accountID, err := pathID(r, "account_id", "找不到这个公众号")
	if err != nil {
		return err
	}
	columnID, err := pathID(r, "column_id", "找不到这个栏目")
	if err != nil {
		return err
	}
	if _, err := a.ownAccount(r.Context(), nil, user.ID, accountID); err != nil {
		return err
	}
	var body columnBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	col, err := normalizeColumn(body, accountID)
	if err != nil {
		return err
	}
	col.ID = columnID
	if body.Status == "suggested" {
		col.Status = "suggested"
	}
	rows, err := a.Store.ListAccountColumns(r.Context(), nil, accountID, "")
	if err != nil {
		return err
	}
	found := false
	for _, row := range rows {
		if row.ID == columnID {
			found = true
			col.SortOrder = row.SortOrder
		}
	}
	if !found {
		return &AppError{Status: 404, Message: "找不到这个栏目"}
	}
	if err := a.Store.UpdateAccountColumn(r.Context(), nil, col); err != nil {
		return err
	}
	writeJSON(w, 200, col)
	return nil
}

func (a *App) deleteColumn(w http.ResponseWriter, r *http.Request, user *model.User) error {
	accountID, err := pathID(r, "account_id", "找不到这个公众号")
	if err != nil {
		return err
	}
	columnID, err := pathID(r, "column_id", "找不到这个栏目")
	if err != nil {
		return err
	}
	if _, err := a.ownAccount(r.Context(), nil, user.ID, accountID); err != nil {
		return err
	}
	if err := a.Store.DeleteAccountColumn(r.Context(), nil, accountID, columnID); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

type confirmColumnsBody struct {
	IDs []int64 `json:"ids"`
}

func (a *App) confirmColumns(w http.ResponseWriter, r *http.Request, user *model.User) error {
	accountID, err := pathID(r, "account_id", "找不到这个公众号")
	if err != nil {
		return err
	}
	if _, err := a.ownAccount(r.Context(), nil, user.ID, accountID); err != nil {
		return err
	}
	var body confirmColumnsBody
	if err := readJSON(r, &body); err != nil && r.ContentLength != 0 {
		return err
	}
	if err := a.Store.ActivateColumns(r.Context(), nil, accountID, body.IDs); err != nil {
		return err
	}
	rows, err := a.Store.ListAccountColumns(r.Context(), nil, accountID, "")
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}

func normalizeColumn(body columnBody, accountID int64) (columns.Column, error) {
	name := strings.TrimSpace(body.Name)
	if name == "" || utf8.RuneCountInString(name) > 20 {
		return columns.Column{}, bad("栏目名请控制在 20 字以内")
	}
	positioning, err := boundedText("栏目定位", body.Positioning, 80)
	if err != nil {
		return columns.Column{}, err
	}
	imageStyle, err := boundedText("配图风格", body.ImageStyle, 40)
	if err != nil {
		return columns.Column{}, err
	}
	wordTarget := body.WordTarget
	if wordTarget == 0 {
		wordTarget = 800
	}
	if wordTarget < 200 || wordTarget > 3000 {
		return columns.Column{}, bad("栏目篇幅请设在 200 到 3000 字")
	}
	weight := body.Weight
	if weight == 0 {
		weight = 1
	}
	if weight < 1 || weight > 3 {
		return columns.Column{}, bad("栏目权重为 1 到 3")
	}
	return columns.Column{
		AccountID: accountID, Name: name, Positioning: positioning,
		Structure: columns.NormalizeStructure(body.Structure), WordTarget: wordTarget,
		ImageStyle: imageStyle, Weight: weight, Status: "active",
	}, nil
}
