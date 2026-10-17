package auth

import (
	"context"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// MemStore is an in-memory Store for tests.
type MemStore struct {
	mu       sync.Mutex
	next     int64
	users    map[int64]User
	sessions map[string]Session
	Consents []string
	Audits   []string
}

func NewMemStore() *MemStore {
	return &MemStore{users: map[int64]User{}, sessions: map[string]Session{}}
}

func (m *MemStore) CreateUser(_ context.Context, n NewUser) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if strings.EqualFold(u.Email, n.Email) {
			return User{}, ErrEmailTaken
		}
	}
	m.next++
	u := User{ID: m.next, Email: n.Email, DisplayName: n.DisplayName, Role: n.Role, Tier: "free", MaxAccounts: 1,
		Status: "active", CreatedAt: time.Now(), PasswordHash: n.PasswordHash}
	m.users[u.ID] = u
	return u, nil
}

func (m *MemStore) UserByEmail(_ context.Context, e string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if strings.EqualFold(u.Email, e) {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (m *MemStore) UserByID(_ context.Context, id int64) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

// Put overwrites a user (tests adjust tier/max_accounts with it).
func (m *MemStore) Put(u User) { m.mu.Lock(); m.users[u.ID] = u; m.mu.Unlock() }

func (m *MemStore) ListStaff(context.Context) ([]User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []User
	for _, u := range m.users {
		if u.Role.IsStaff() {
			out = append(out, u)
		}
	}
	return out, nil
}

func (m *MemStore) SetStatus(_ context.Context, id int64, st string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return ErrNotFound
	}
	u.Status = st
	m.users[id] = u
	return nil
}

func (m *MemStore) CountRole(_ context.Context, r Role) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, u := range m.users {
		if u.Role == r {
			n++
		}
	}
	return n, nil
}

func (m *MemStore) CreateSession(_ context.Context, s Session) error {
	m.mu.Lock()
	m.sessions[hex.EncodeToString(s.TokenHash)] = s
	m.mu.Unlock()
	return nil
}

func (m *MemStore) SessionUser(_ context.Context, h []byte, now time.Time) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[hex.EncodeToString(h)]
	if !ok || !now.Before(s.ExpiresAt) {
		return User{}, ErrNotFound
	}
	u, ok := m.users[s.UserID]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *MemStore) DeleteSession(_ context.Context, h []byte) error {
	m.mu.Lock()
	delete(m.sessions, hex.EncodeToString(h))
	m.mu.Unlock()
	return nil
}

func (m *MemStore) DeleteUserSessions(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, s := range m.sessions {
		if s.UserID == id {
			delete(m.sessions, k)
		}
	}
	return nil
}

func (m *MemStore) RecordConsent(_ context.Context, id int64, doc, ver, _, _ string) error {
	m.mu.Lock()
	m.Consents = append(m.Consents, doc+"@"+ver)
	m.mu.Unlock()
	return nil
}

func (m *MemStore) Audit(_ context.Context, _ *int64, action, target string, _ map[string]any) error {
	m.mu.Lock()
	m.Audits = append(m.Audits, action+":"+target)
	m.mu.Unlock()
	return nil
}
