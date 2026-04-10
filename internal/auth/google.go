package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	AdminEmails  string
	CookieSecret string
}

type Service struct {
	oauth2Config *oauth2.Config
	verifier     *oidc.IDTokenVerifier
	adminEmails  map[string]bool
	cookieSecret []byte
}

type UserInfo struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

func New(ctx context.Context, cfg Config) (*Service, error) {
	provider, err := oidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		return nil, fmt.Errorf("oidc provider: %w", err)
	}

	oauth2Config := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Endpoint:     google.Endpoint,
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})

	admins := make(map[string]bool)
	for _, email := range strings.Split(cfg.AdminEmails, ",") {
		email = strings.TrimSpace(email)
		if email != "" {
			admins[email] = true
		}
	}

	return &Service{
		oauth2Config: oauth2Config,
		verifier:     verifier,
		adminEmails:  admins,
		cookieSecret: []byte(cfg.CookieSecret),
	}, nil
}

func (s *Service) LoginHandler(w http.ResponseWriter, r *http.Request) {
	state, err := generateState()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		MaxAge:   600,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
	})

	url := s.oauth2Config.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (s *Service) CallbackHandler(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	token, err := s.oauth2Config.Exchange(r.Context(), code)
	if err != nil {
		slog.Error("token exchange failed", "error", err)
		http.Error(w, "auth failed", http.StatusUnauthorized)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "no id_token", http.StatusUnauthorized)
		return
	}

	idToken, err := s.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		slog.Error("token verify failed", "error", err)
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	var info UserInfo
	if err := idToken.Claims(&info); err != nil {
		http.Error(w, "failed to parse claims", http.StatusInternalServerError)
		return
	}

	if len(s.adminEmails) > 0 && !s.adminEmails[info.Email] {
		slog.Warn("unauthorized login attempt", "email", info.Email)
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}

	sessionData, _ := json.Marshal(info)
	signed := s.signCookie(string(sessionData))

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    signed,
		MaxAge:   86400 * 7,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
	})

	// Clear state cookie
	http.SetCookie(w, &http.Cookie{
		Name:   "oauth_state",
		MaxAge: -1,
		Path:   "/",
	})

	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.ValidateSession(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Service) ValidateSession(r *http.Request) (*UserInfo, bool) {
	cookie, err := r.Cookie("session")
	if err != nil {
		return nil, false
	}

	data, ok := s.verifyCookie(cookie.Value)
	if !ok {
		return nil, false
	}

	var user UserInfo
	if err := json.Unmarshal([]byte(data), &user); err != nil {
		return nil, false
	}
	return &user, true
}

func (s *Service) signCookie(data string) string {
	mac := hmac.New(sha256.New, s.cookieSecret)
	mac.Write([]byte(data))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	encoded := base64.RawURLEncoding.EncodeToString([]byte(data))
	return encoded + "." + sig
}

func (s *Service) verifyCookie(value string) (string, bool) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return "", false
	}

	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}

	mac := hmac.New(sha256.New, s.cookieSecret)
	mac.Write(data)
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(parts[1]), []byte(expectedSig)) {
		return "", false
	}
	return string(data), true
}

type contextKey string

const userContextKey contextKey = "user"

func UserFromContext(ctx context.Context) *UserInfo {
	u, _ := ctx.Value(userContextKey).(*UserInfo)
	return u
}

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LogoutHandler clears the session cookie.
func (s *Service) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
	})
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"logged_out"}`))
}

// TokenFromQuery extracts a session token from query parameter for SSE connections.
// Since SSE (EventSource) cannot set custom headers, we pass the session cookie value as a query param.
func (s *Service) TokenFromQuery(r *http.Request) (*UserInfo, bool) {
	token := r.URL.Query().Get("token")
	if token == "" {
		return nil, false
	}
	data, ok := s.verifyCookie(token)
	if !ok {
		return nil, false
	}
	var user UserInfo
	if err := json.Unmarshal([]byte(data), &user); err != nil {
		return nil, false
	}
	return &user, true
}

// RequireAuthOrToken checks cookie first, then query param token (for SSE).
func (s *Service) RequireAuthOrToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.ValidateSession(r)
		if !ok {
			user, ok = s.TokenFromQuery(r)
		}
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

var _ = time.Now // ensure time import used
