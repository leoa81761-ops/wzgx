package account

import (
	"context"
	"net/http"
	"strings"

	"gonghao/internal/auth"
	"gonghao/internal/crypto"
	"gonghao/internal/server"
)

// SetTavilyKey stores the user's own Tavily key (encrypted, masked). Empty key removes it.
func (s *Service) SetTavilyKey(ctx context.Context, actor auth.User, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		_, err := s.DB.Exec(ctx, `DELETE FROM user_secret WHERE user_id=$1 AND kind='tavily'`, actor.ID)
		s.Audit.Log(ctx, actor.ID, "secret.tavily", "", map[string]any{"key": "cleared"})
		return "", err
	}
	if len(key) < 10 || len(key) > 200 {
		return "", ErrInvalidInput
	}
	sealed, err := s.Box.Seal(key)
	if err != nil {
		return "", err
	}
	mask := crypto.Mask(key)
	_, err = s.DB.Exec(ctx, `INSERT INTO user_secret (user_id, kind, cipher, mask) VALUES ($1,'tavily',$2,$3)
		ON CONFLICT (user_id, kind) DO UPDATE SET cipher=$2, mask=$3, updated_at=now()`, actor.ID, sealed, mask)
	if err != nil {
		return "", err
	}
	s.Audit.Log(ctx, actor.ID, "secret.tavily", "", map[string]any{"key": "replaced"})
	return mask, nil
}

func (s *Service) hTavily(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if decode(w, r, &in) {
		m, err := s.SetTavilyKey(r.Context(), actor(r), in.Key)
		reply(w, 200, map[string]string{"mask": m}, err)
	}
}

func (s *Service) hTavilyGet(w http.ResponseWriter, r *http.Request) {
	var m string
	s.DB.QueryRow(r.Context(), `SELECT mask FROM user_secret WHERE user_id=$1 AND kind='tavily'`, actor(r).ID).Scan(&m)
	server.WriteJSON(w, 200, map[string]any{"mask": m, "has_key": m != ""})
}
