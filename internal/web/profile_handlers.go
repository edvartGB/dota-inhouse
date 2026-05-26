package web

import (
	"log"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/edvart/dota-inhouse/internal/auth"
)

const maxDisplayNameLen = 32

type ProfilePageData struct {
	User        interface{}
	DisplayName string
	Error       string
	IsAdmin     bool
}

func (s *Server) handleProfilePage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	data := ProfilePageData{
		User:        user,
		DisplayName: user.Name,
		Error:       r.URL.Query().Get("error"),
		IsAdmin:     s.adminConfig.IsAdmin(user.SteamID),
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

	if err := s.store.UpdateUserProfile(r.Context(), user.SteamID, displayName); err != nil {
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

func redirectProfileError(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/profile?error="+url.QueryEscape(msg), http.StatusSeeOther)
}
