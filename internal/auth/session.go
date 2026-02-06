package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/edvart/dota-inhouse/internal/store"
)

const (
	SessionCookieName = "session_id"
	SessionDuration   = 7 * 24 * time.Hour
)

// SessionManager handles user sessions.
type SessionManager struct {
	store store.Store
}

// NewSessionManager creates a new session manager.
func NewSessionManager(store store.Store) *SessionManager {
	return &SessionManager{store: store}
}

// CreateSession creates a new session for a user and sets the cookie.
func (sm *SessionManager) CreateSession(ctx context.Context, w http.ResponseWriter, steamID string) error {
	sessionID, err := generateSessionID()
	if err != nil {
		return err
	}

	session := &store.Session{
		ID:        sessionID,
		SteamID:   steamID,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(SessionDuration),
	}

	if err := sm.store.CreateSession(ctx, session); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		Expires:  session.ExpiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	return nil
}

// GetSession retrieves the session from the request cookie.
func (sm *SessionManager) GetSession(ctx context.Context, r *http.Request) (*store.Session, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil, nil // No cookie = no session
	}

	return sm.store.GetSession(ctx, cookie.Value)
}

// GetUser retrieves the user for the current session.
func (sm *SessionManager) GetUser(ctx context.Context, r *http.Request) (*store.User, error) {
	session, err := sm.GetSession(ctx, r)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, nil
	}

	return sm.store.GetUser(ctx, session.SteamID)
}

// DeleteSession removes the current session.
func (sm *SessionManager) DeleteSession(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil
	}

	if err := sm.store.DeleteSession(ctx, cookie.Value); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	return nil
}

func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
