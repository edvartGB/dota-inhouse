package web

import (
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/edvart/dota-inhouse/internal/auth"
)

const maxDisplayNameLen = 32
const maxDiscordUsernameLen = 64

var discordUserIDRegex = regexp.MustCompile(`^\d{17,20}$`)

type ProfilePageData struct {
	User            interface{}
	DisplayName     string
	DiscordUsername string
	DiscordUserID   string
	Error           string
}

func (s *Server) handleProfilePage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	data := ProfilePageData{
		User:            user,
		DisplayName:     user.Name,
		DiscordUsername: user.DiscordUsername,
		DiscordUserID:   user.DiscordUserID,
		Error:           r.URL.Query().Get("error"),
	}

	if err := s.templates.ExecuteTemplate(w, "profile.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (s *Server) handleProfileUpdate(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		redirectProfileError(w, r, "Invalid form data")
		return
	}

	displayName := strings.TrimSpace(r.FormValue("display_name"))
	if errMsg := validateDisplayName(displayName); errMsg != "" {
		redirectProfileError(w, r, errMsg)
		return
	}

	discordUsername := normalizeDiscordUsername(r.FormValue("discord_username"))
	if errMsg := validateDiscordUsername(discordUsername); errMsg != "" {
		redirectProfileError(w, r, errMsg)
		return
	}

	discordUserID := strings.TrimSpace(r.FormValue("discord_user_id"))
	if errMsg := validateDiscordUserID(discordUserID); errMsg != "" {
		redirectProfileError(w, r, errMsg)
		return
	}

	if err := s.store.UpdateUserProfile(r.Context(), user.SteamID, displayName, discordUsername, discordUserID); err != nil {
		log.Printf("Failed to update profile for %s: %v", user.SteamID, err)
		redirectProfileError(w, r, "Failed to update profile")
		return
	}

	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}

func validateDisplayName(displayName string) string {
	if displayName == "" {
		return "Display name cannot be empty"
	}
	if utf8.RuneCountInString(displayName) > maxDisplayNameLen {
		return "Display name must be 32 characters or less"
	}
	return ""
}

func normalizeDiscordUsername(discordUsername string) string {
	discordUsername = strings.TrimSpace(discordUsername)
	discordUsername = strings.TrimPrefix(discordUsername, "@")
	return discordUsername
}

func validateDiscordUsername(discordUsername string) string {
	if discordUsername == "" {
		return ""
	}
	if utf8.RuneCountInString(discordUsername) > maxDiscordUsernameLen {
		return "Discord username must be 64 characters or less"
	}
	return ""
}

func validateDiscordUserID(discordUserID string) string {
	if discordUserID == "" {
		return ""
	}
	if !discordUserIDRegex.MatchString(discordUserID) {
		return "Discord user ID must be 17-20 digits"
	}
	return ""
}

func redirectProfileError(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/profile?error="+url.QueryEscape(msg), http.StatusSeeOther)
}
