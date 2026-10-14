package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web/*
var assets embed.FS

type session struct {
	UserID  string
	Expires time.Time
}
type attempt struct {
	Count int
	Start time.Time
}
type application struct {
	store    *store
	mu       sync.Mutex
	sessions map[string]session
	attempts map[string]attempt
}

func newApplication(s *store) *application {
	return &application{store: s, sessions: map[string]session{}, attempts: map[string]attempt{}}
}

func main() {
	dataDir := env("DATA_DIR", "./data")
	s, err := openStore(filepath.Join(dataDir, "workspace.json"))
	if err != nil {
		log.Fatal(err)
	}
	a := newApplication(s)
	if password := os.Getenv("ADMIN_PASSWORD"); password != "" {
		s.mu.RLock()
		empty := len(s.db.Users) == 0
		s.mu.RUnlock()
		if empty {
			if err := a.bootstrap(env("ADMIN_USERNAME", "admin"), env("ADMIN_NAME", "Administrator"), password); err != nil {
				log.Fatal(err)
			}
		}
	}
	server := &http.Server{Addr: ":" + env("PORT", "8111"), Handler: a.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()
	log.Printf("L10 listening on %s; data: %s", server.Addr, dataDir)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func (a *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/session", a.sessionInfo)
	mux.HandleFunc("POST /api/setup", a.setup)
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", a.logout)
	mux.HandleFunc("POST /api/password", a.protected(a.changePassword))
	mux.HandleFunc("GET /api/teams", a.protected(a.listTeams))
	mux.HandleFunc("POST /api/teams", a.admin(a.createTeam))
	mux.HandleFunc("PUT /api/teams/{team}/members", a.admin(a.setMembers))
	mux.HandleFunc("GET /api/teams/{team}/state", a.protected(a.getState))
	mux.HandleFunc("PUT /api/teams/{team}/state", a.protected(a.saveState))
	mux.HandleFunc("POST /api/transfer", a.protected(a.transfer))
	mux.HandleFunc("GET /api/users", a.admin(a.listUsers))
	mux.HandleFunc("POST /api/users", a.admin(a.createUser))
	mux.HandleFunc("PUT /api/users/{user}", a.admin(a.updateUser))
	mux.HandleFunc("GET /api/export", a.admin(a.export))
	mux.HandleFunc("POST /api/import", a.admin(a.importBackup))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeError(w, 404, "API endpoint not found") })
	web, _ := fs.Sub(assets, "web")
	mux.Handle("/", http.FileServer(http.FS(web)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeError(w, 403, "Cross-origin requests are not allowed")
					return
				}
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (a *application) currentUser(r *http.Request) (User, bool) {
	c, err := r.Cookie("l10_session")
	if err != nil {
		return User{}, false
	}
	a.mu.Lock()
	s, ok := a.sessions[c.Value]
	if ok && !s.Expires.After(time.Now()) {
		delete(a.sessions, c.Value)
		ok = false
	}
	a.mu.Unlock()
	if !ok {
		return User{}, false
	}
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	for _, u := range a.store.db.Users {
		if u.ID == s.UserID && !u.Disabled {
			return u, true
		}
	}
	return User{}, false
}
func (a *application) protected(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.currentUser(r); !ok {
			writeError(w, 401, "Please sign in to continue.")
			return
		}
		next(w, r)
	}
}
func (a *application) admin(next http.HandlerFunc) http.HandlerFunc {
	return a.protected(func(w http.ResponseWriter, r *http.Request) {
		u, _ := a.currentUser(r)
		if u.Role != "admin" {
			writeError(w, 403, "Administrator access required")
			return
		}
		next(w, r)
	})
}
func canAccess(u User, t State) bool {
	if u.Role == "admin" {
		return true
	}
	for _, m := range t.Members {
		if m.ID == u.ID {
			return true
		}
	}
	return false
}
func (a *application) sessionInfo(w http.ResponseWriter, r *http.Request) {
	u, ok := a.currentUser(r)
	a.store.mu.RLock()
	setup := len(a.store.db.Users) == 0
	a.store.mu.RUnlock()
	writeJSON(w, 200, map[string]any{"authenticated": ok, "setupRequired": setup, "user": publicUser(u)})
}
func (a *application) setSession(w http.ResponseWriter, r *http.Request, u User) {
	token := newID() + newID()
	a.mu.Lock()
	for k, s := range a.sessions {
		if s.Expires.Before(time.Now()) {
			delete(a.sessions, k)
		}
	}
	a.sessions[token] = session{u.ID, time.Now().Add(7 * 24 * time.Hour)}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "l10_session", Value: token, Path: "/", MaxAge: 7 * 24 * 60 * 60, HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode})
}
func (a *application) invalidate(userID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, s := range a.sessions {
		if s.UserID == userID {
			delete(a.sessions, key)
		}
	}
}
func (a *application) limited(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, v := range a.attempts {
		if time.Since(v.Start) > 15*time.Minute {
			delete(a.attempts, key)
		}
	}
	v := a.attempts[host]
	if v.Start.IsZero() {
		v.Start = time.Now()
	}
	v.Count++
	a.attempts[host] = v
	return v.Count > 20
}
func (a *application) bootstrap(username, name, password string) error {
	salt, hash, err := hashPassword(password)
	if err != nil {
		return fmtInvalid(err.Error())
	}
	return a.store.update(func(db *Database) error {
		if len(db.Users) != 0 {
			return fmtInvalid("setup has already been completed")
		}
		u := User{ID: newID(), Name: strings.TrimSpace(name), Username: strings.ToLower(strings.TrimSpace(username)), Role: "admin", Salt: salt, PasswordHash: hash}
		db.Users = append(db.Users, u)
		for i := range db.Teams {
			db.Teams[i].Members = append(db.Teams[i].Members, Member{u.ID, u.Name})
			db.Teams[i].Version++
		}
		return nil
	})
}
func (a *application) setup(w http.ResponseWriter, r *http.Request) {
	if a.limited(r) {
		writeError(w, 429, "Too many attempts. Try again in 15 minutes.")
		return
	}
	var input struct {
		Username string `json:"username"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &input); err != nil {
		writeError(w, 400, "Invalid account details")
		return
	}
	if err := a.bootstrap(input.Username, input.Name, input.Password); err != nil {
		respondError(w, err)
		return
	}
	a.store.mu.RLock()
	u := a.store.db.Users[0]
	a.store.mu.RUnlock()
	a.setSession(w, r, u)
	writeJSON(w, 201, publicUser(u))
}
func (a *application) login(w http.ResponseWriter, r *http.Request) {
	if a.limited(r) {
		writeError(w, 429, "Too many attempts. Try again in 15 minutes.")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &input); err != nil {
		writeError(w, 400, "Enter a username and password")
		return
	}
	a.store.mu.RLock()
	var user User
	for _, u := range a.store.db.Users {
		if u.Username == strings.ToLower(strings.TrimSpace(input.Username)) {
			user = u
			break
		}
	}
	a.store.mu.RUnlock()
	if !checkPassword(user, input.Password) || user.ID == "" || user.Disabled {
		writeError(w, 401, "Incorrect username or password")
		return
	}
	a.setSession(w, r, user)
	writeJSON(w, 200, publicUser(user))
}
func (a *application) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("l10_session"); err == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "l10_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *application) changePassword(w http.ResponseWriter, r *http.Request) {
	if a.limited(r) {
		writeError(w, 429, "Too many attempts. Try again in 15 minutes.")
		return
	}
	var input struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &input); err != nil {
		writeError(w, 400, "Invalid password details")
		return
	}
	u, _ := a.currentUser(r)
	if !checkPassword(u, input.Current) {
		writeError(w, 400, "Current password is incorrect")
		return
	}
	salt, hash, err := hashPassword(input.Password)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	err = a.store.update(func(db *Database) error {
		for i := range db.Users {
			if db.Users[i].ID == u.ID {
				db.Users[i].Salt = salt
				db.Users[i].PasswordHash = hash
				return nil
			}
		}
		return errNotFound
	})
	if err != nil {
		respondError(w, err)
		return
	}
	a.invalidate(u.ID)
	a.setSession(w, r, u)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

var errNotFound = errors.New("not found")
var errForbidden = errors.New("access denied")

func fmtInvalid(message string) error { return errors.Join(errInvalid, errors.New(message)) }
func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errConflict):
		writeError(w, 409, "Data changed in another window. Refresh and try again.")
	case errors.Is(err, errNotFound):
		writeError(w, 404, "Item or team not found")
	case errors.Is(err, errForbidden):
		writeError(w, 403, "You do not have access to this team")
	case errors.Is(err, errInvalid):
		writeError(w, 400, err.Error())
	default:
		log.Printf("request: %v", err)
		writeError(w, 500, "Could not save changes. Please try again.")
	}
}
func decode(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("response: %v", err)
	}
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
