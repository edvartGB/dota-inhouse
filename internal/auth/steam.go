package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/edvart/dota-inhouse/internal/store"
	"github.com/yohcop/openid-go"
)

const (
	steamOpenIDEndpoint = "https://steamcommunity.com/openid"
	steamAPIURL         = "https://api.steampowered.com/ISteamUser/GetPlayerSummaries/v0002/"
)

var steamIDRegex = regexp.MustCompile(`https://steamcommunity\.com/openid/id/(\d+)`)

// SteamAuth handles Steam OpenID authentication.
type SteamAuth struct {
	apiKey         string
	baseURL        string
	store          store.Store
	sessions       *SessionManager
	nonceStore     openid.NonceStore
	discoveryCache openid.DiscoveryCache
}

// SteamUser represents user data from Steam API.
type SteamUser struct {
	SteamID     string `json:"steamid"`
	PersonaName string `json:"personaname"`
	AvatarURL   string `json:"avatarfull"`
}

// NewSteamAuth creates a new Steam authentication handler.
func NewSteamAuth(apiKey, baseURL string, store store.Store, sessions *SessionManager) *SteamAuth {
	return &SteamAuth{
		apiKey:         apiKey,
		baseURL:        baseURL,
		store:          store,
		sessions:       sessions,
		nonceStore:     openid.NewSimpleNonceStore(),
		discoveryCache: openid.NewSimpleDiscoveryCache(),
	}
}

// LoginHandler redirects to Steam's OpenID login.
func (sa *SteamAuth) LoginHandler(w http.ResponseWriter, r *http.Request) {
	callbackURL := sa.baseURL + "/auth/callback"

	authURL, err := openid.RedirectURL(
		steamOpenIDEndpoint,
		callbackURL,
		sa.baseURL,
	)
	if err != nil {
		http.Error(w, "Failed to create auth URL", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, authURL, http.StatusFound)
}

// CallbackHandler handles the OpenID callback from Steam.
func (sa *SteamAuth) CallbackHandler(w http.ResponseWriter, r *http.Request) {
	fullURL := sa.baseURL + r.URL.String()

	id, err := openid.Verify(fullURL, sa.discoveryCache, sa.nonceStore)
	if err != nil {
		http.Error(w, "OpenID verification failed: "+err.Error(), http.StatusUnauthorized)
		return
	}

	// Extract Steam ID from claimed ID
	matches := steamIDRegex.FindStringSubmatch(id)
	if len(matches) != 2 {
		http.Error(w, "Invalid Steam ID in response", http.StatusBadRequest)
		return
	}
	steamID := matches[1]

	// Fetch user info from Steam API
	steamUser, err := sa.fetchSteamUser(r.Context(), steamID)
	if err != nil {
		http.Error(w, "Failed to fetch Steam user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Create or update user in database
	now := time.Now()
	user := &store.User{
		SteamID:         steamUser.SteamID,
		Name:            steamUser.PersonaName,
		AvatarURL:       steamUser.AvatarURL,
		CaptainPriority: 5, // Default priority
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := sa.store.UpsertUser(r.Context(), user); err != nil {
		http.Error(w, "Failed to save user", http.StatusInternalServerError)
		return
	}

	// Create session
	if err := sa.sessions.CreateSession(r.Context(), w, steamID); err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	// Redirect to home
	http.Redirect(w, r, "/", http.StatusFound)
}

// LogoutHandler logs out the user.
func (sa *SteamAuth) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	sa.sessions.DeleteSession(r.Context(), w, r)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (sa *SteamAuth) fetchSteamUser(ctx context.Context, steamID string) (*SteamUser, error) {
	reqURL := fmt.Sprintf("%s?key=%s&steamids=%s", steamAPIURL, sa.apiKey, steamID)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("steam API returned status %d", resp.StatusCode)
	}

	var result struct {
		Response struct {
			Players []SteamUser `json:"players"`
		} `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Response.Players) == 0 {
		return nil, fmt.Errorf("no player data returned")
	}

	return &result.Response.Players[0], nil
}

// DevLoginHandler provides a development-only login mechanism.
func (sa *SteamAuth) DevLoginHandler(w http.ResponseWriter, r *http.Request) {
	steamID := r.URL.Query().Get("steamid")
	name := r.URL.Query().Get("name")

	if steamID == "" || name == "" {
		http.Error(w, "steamid and name required", http.StatusBadRequest)
		return
	}

	// Create user
	now := time.Now()
	user := &store.User{
		SteamID:         steamID,
		Name:            name,
		AvatarURL:       "",
		CaptainPriority: 5,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := sa.store.UpsertUser(r.Context(), user); err != nil {
		http.Error(w, "Failed to save user", http.StatusInternalServerError)
		return
	}

	// Create session
	if err := sa.sessions.CreateSession(r.Context(), w, steamID); err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

// MeHandler returns the current user's info.
func (sa *SteamAuth) MeHandler(w http.ResponseWriter, r *http.Request) {
	user, err := sa.sessions.GetUser(r.Context(), r)
	if err != nil {
		http.Error(w, "Failed to get user", http.StatusInternalServerError)
		return
	}

	if user == nil {
		http.Error(w, "Not logged in", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"steamId":   user.SteamID,
		"name":      user.Name,
		"avatarUrl": user.AvatarURL,
	})
}

// RequireAuth middleware ensures the request has a valid session.
func RequireAuth(sessions *SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := sessions.GetUser(r.Context(), r)
			if err != nil || user == nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Store user in context
			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type contextKey string

const userContextKey contextKey = "user"

// UserFromContext retrieves the user from the request context.
func UserFromContext(ctx context.Context) *store.User {
	user, _ := ctx.Value(userContextKey).(*store.User)
	return user
}

// CreateFakeUsers creates fake users for development.
func (sa *SteamAuth) CreateFakeUsers(ctx context.Context, count int) error {
	now := time.Now()
	for i := 1; i <= count; i++ {
		user := &store.User{
			SteamID:         fmt.Sprintf("fake_%d", i),
			Name:            fmt.Sprintf("Player %d", i),
			AvatarURL:       "",
			CaptainPriority: 5,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := sa.store.UpsertUser(ctx, user); err != nil {
			return err
		}
	}
	return nil
}

// SteamIDFromOpenIDURL extracts the Steam ID from an OpenID identity URL.
func SteamIDFromOpenIDURL(idURL string) string {
	// Handle both formats
	if strings.HasPrefix(idURL, "https://steamcommunity.com/openid/id/") {
		return strings.TrimPrefix(idURL, "https://steamcommunity.com/openid/id/")
	}
	if strings.HasPrefix(idURL, "http://steamcommunity.com/openid/id/") {
		return strings.TrimPrefix(idURL, "http://steamcommunity.com/openid/id/")
	}
	return ""
}
