package auth

import (
	"net/http"
	"strings"
)

// AdminConfig holds admin configuration.
type AdminConfig struct {
	AdminSteamIDs map[string]bool
}

// NewAdminConfig creates admin config from comma-separated Steam IDs.
func NewAdminConfig(steamIDs string) *AdminConfig {
	cfg := &AdminConfig{
		AdminSteamIDs: make(map[string]bool),
	}

	for _, id := range strings.Split(steamIDs, ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			cfg.AdminSteamIDs[id] = true
		}
	}

	return cfg
}

// IsAdmin checks if a Steam ID is an admin.
func (c *AdminConfig) IsAdmin(steamID string) bool {
	return c.AdminSteamIDs[steamID]
}

// AdminMiddleware creates middleware that requires admin access.
func AdminMiddleware(cfg *AdminConfig, sessions *SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := sessions.GetUser(r.Context(), r)
			if err != nil || user == nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			if !cfg.IsAdmin(user.SteamID) {
				http.Error(w, "Forbidden: Admin access required", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
