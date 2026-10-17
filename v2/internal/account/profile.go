package account

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"gonghao/internal/auth"
)

// Profile holds the account's writing settings. All text is passed to the model as written;
// the program adds no style rules of its own.
type Profile struct {
	AccountID   int64     `json:"account_id"`
	Positioning string    `json:"positioning"`
	Persona     string    `json:"persona"`
	Audience    string    `json:"audience"`
	Tone        string    `json:"tone"`
	Taboos      string    `json:"taboos"`
	Signature   string    `json:"signature"`
	Opening     string    `json:"opening"`
	Ending      string    `json:"ending"`
	AILabel     bool      `json:"ai_label"`
	SelfReview  bool      `json:"self_review"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const maxField = 4000

func (s *Service) GetProfile(ctx context.Context, actor auth.User, accountID int64) (Profile, error) {
	if _, err := s.Get(ctx, actor, accountID); err != nil {
		return Profile{}, err
	}
	var p Profile
	err := s.DB.QueryRow(ctx, `SELECT account_id, positioning, persona, audience, tone, taboos, signature, opening, ending, ai_label, self_review, updated_at
		FROM account_profile WHERE account_id=$1`, accountID).
		Scan(&p.AccountID, &p.Positioning, &p.Persona, &p.Audience, &p.Tone, &p.Taboos, &p.Signature, &p.Opening, &p.Ending, &p.AILabel, &p.SelfReview, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{AccountID: accountID}, nil
	}
	return p, err
}

// SaveProfile replaces the whole profile (owner or staff).
func (s *Service) SaveProfile(ctx context.Context, actor auth.User, accountID int64, p Profile) (Profile, error) {
	a, err := s.Get(ctx, actor, accountID)
	if err != nil {
		return p, err
	}
	for _, f := range []string{p.Positioning, p.Persona, p.Audience, p.Tone, p.Taboos, p.Signature, p.Opening, p.Ending} {
		if len([]rune(f)) > maxField {
			return p, errors.New("单项设置不能超过 4000 字")
		}
	}
	p.AccountID = accountID
	err = s.DB.QueryRow(ctx, `INSERT INTO account_profile (account_id, positioning, persona, audience, tone, taboos, signature, opening, ending, ai_label, self_review, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())
		ON CONFLICT (account_id) DO UPDATE SET positioning=$2, persona=$3, audience=$4, tone=$5, taboos=$6, signature=$7, opening=$8,
		ending=$9, ai_label=$10, self_review=$11, updated_at=now() RETURNING updated_at`,
		accountID, p.Positioning, p.Persona, p.Audience, p.Tone, p.Taboos, p.Signature, p.Opening, p.Ending, p.AILabel, p.SelfReview).Scan(&p.UpdatedAt)
	if err != nil {
		return p, err
	}
	s.Audit.Log(ctx, actor.ID, "account.profile", a.AppID, map[string]any{"account_id": accountID})
	return p, nil
}
