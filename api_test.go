package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

type testClient struct {
	t       *testing.T
	handler http.Handler
	cookie  *http.Cookie
}

func (c *testClient) request(method, path string, body any, status int, result any) {
	c.t.Helper()
	var buffer bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buffer).Encode(body); err != nil {
			c.t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, &buffer)
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	w := httptest.NewRecorder()
	c.handler.ServeHTTP(w, r)
	if w.Code != status {
		c.t.Fatalf("%s %s: got %d, want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "l10_session" {
			c.cookie = cookie
		}
	}
	if result != nil {
		if err := json.Unmarshal(w.Body.Bytes(), result); err != nil {
			c.t.Fatal(err)
		}
	}
}
func TestAccountsTeamsTransfersAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	a := newApplication(s)
	admin := testClient{t: t, handler: a.routes()}
	member := testClient{t: t, handler: a.routes()}
	member.request("GET", "/api/teams", nil, 401, nil)
	var root User
	admin.request("POST", "/api/setup", map[string]string{"username": "admin", "name": "Administrator", "password": "strong-password-123"}, 201, &root)
	admin.request("POST", "/api/setup", map[string]string{"username": "second", "name": "Second", "password": "strong-password-123"}, 400, nil)
	var bob User
	admin.request("POST", "/api/users", userInput{Name: "Bob", Username: "bob", Role: "member", Password: "bob-password-123"}, 201, &bob)
	if bob.PasswordHash != "" || bob.Salt != "" {
		t.Fatal("password hashes leaked")
	}
	member.request("POST", "/api/login", map[string]string{"username": "bob", "password": "wrong"}, 401, nil)
	member.request("POST", "/api/login", map[string]string{"username": "BOB", "password": "bob-password-123"}, 200, nil)
	member.request("GET", "/api/users", nil, 403, nil)
	member.request("GET", "/api/export", nil, 403, nil)
	var teams []State
	admin.request("GET", "/api/teams", nil, 200, &teams)
	source := teams[0]
	member.request("GET", "/api/teams/"+source.ID+"/state", nil, 403, nil)
	admin.request("PUT", "/api/teams/"+source.ID+"/members", map[string]any{"members": []string{root.ID, bob.ID}, "version": source.Version}, 200, &source)
	var destination State
	admin.request("POST", "/api/teams", map[string]any{"name": "Operations", "members": []string{root.ID}}, 201, &destination)
	member.request("GET", "/api/teams/"+source.ID+"/state", nil, 200, nil)
	member.request("POST", "/api/teams", map[string]any{"name": "Unauthorized"}, 403, nil)
	source.Metrics = append(source.Metrics, Metric{ID: "metric", Name: "Revenue", Owner: root.ID, Assignees: []string{root.ID, bob.ID}, Direction: "gte", Goal: 100, Values: map[string]float64{"2026-10-12": 120}})
	source.Rocks = append(source.Rocks, Rock{ID: "rock", Title: "Launch", Owner: bob.ID, Assignees: []string{root.ID, bob.ID}, Status: "on-track", Due: "2026-12-31"})
	source.Todos = append(source.Todos, Todo{ID: "todo", Title: "Follow up", Owner: bob.ID, Assignees: []string{root.ID, bob.ID}, Due: "2026-10-21"})
	source.Issues = append(source.Issues, Issue{ID: "issue", Title: "Pipeline", Owner: bob.ID, Assignees: []string{bob.ID}, Priority: 1, Status: "open"})
	source.Headlines = append(source.Headlines, Headline{ID: "headline", Title: "New customer", Kind: "customer", Assignees: []string{bob.ID}})
	old := clone(source)
	admin.request("PUT", "/api/teams/"+source.ID+"/state", source, 200, &source)
	admin.request("PUT", "/api/teams/"+source.ID+"/state", old, 409, nil)
	bad := clone(source)
	bad.Members = []Member{}
	member.request("PUT", "/api/teams/"+source.ID+"/state", bad, 400, nil)
	transfer := transferInput{Source: source.ID, Destination: destination.ID, Version: source.Version, DestinationVersion: destination.Version, Items: map[string][]string{"todos": {"todo"}}}
	member.request("POST", "/api/transfer", transfer, 403, nil)
	admin.request("POST", "/api/transfer", transfer, 200, nil)
	admin.request("GET", "/api/teams/"+source.ID+"/state", nil, 200, &source)
	admin.request("GET", "/api/teams/"+destination.ID+"/state", nil, 200, &destination)
	if len(source.Todos) != 0 || len(destination.Todos) != 1 || len(destination.Todos[0].Assignees) != 2 || !canAccess(bob, destination) {
		t.Fatal("transfer lost data, assignments or membership")
	}
	member.request("GET", "/api/teams/"+destination.ID+"/state", nil, 200, nil)
	transfer.Version = source.Version
	transfer.DestinationVersion = destination.Version
	transfer.Items = map[string][]string{"rocks": {"rock"}, "todos": {"missing"}}
	admin.request("POST", "/api/transfer", transfer, 404, nil)
	unchanged, _ := s.get(source.ID)
	if len(unchanged.Rocks) != 1 || unchanged.Version != source.Version {
		t.Fatal("failed transfer partially committed")
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	source.Meetings = append(source.Meetings, Meeting{ID: "meeting", StartedAt: stamp, StageSeconds: make([]float64, 7), Attendees: []string{bob.ID}, Ratings: map[string]int{bob.ID: 9}, TimerStartedAt: stamp})
	admin.request("PUT", "/api/teams/"+source.ID+"/state", source, 200, &source)
	transfer.Version = source.Version
	transfer.Items = nil
	transfer.All = true
	admin.request("POST", "/api/transfer", transfer, 400, nil)
	source.Meetings[0].EndedAt = stamp
	source.Meetings[0].TimerStartedAt = ""
	source.Meetings[0].Snapshot = &Snapshot{Members: source.Members, Metrics: source.Metrics, Rocks: source.Rocks, Todos: source.Todos, Issues: source.Issues, Headlines: source.Headlines}
	admin.request("PUT", "/api/teams/"+source.ID+"/state", source, 200, &source)
	bad = clone(source)
	bad.Meetings[0].Notes = "modified history"
	admin.request("PUT", "/api/teams/"+source.ID+"/state", bad, 400, nil)
	transfer.Version = source.Version
	admin.request("POST", "/api/transfer", transfer, 200, nil)
	admin.request("GET", "/api/teams/"+source.ID+"/state", nil, 200, &source)
	admin.request("GET", "/api/teams/"+destination.ID+"/state", nil, 200, &destination)
	if len(source.Metrics)+len(source.Rocks)+len(source.Issues)+len(source.Headlines)+len(source.Meetings) != 0 || len(destination.Meetings) != 1 || destination.Meetings[0].Snapshot.Members[1].Name != "Bob" {
		t.Fatal("bulk transfer or snapshot preservation failed")
	}
	admin.request("PUT", "/api/teams/"+destination.ID+"/members", map[string]any{"members": []string{root.ID}, "version": destination.Version}, 200, &destination)
	if destination.Todos[0].Owner != "" || len(destination.Todos[0].Assignees) != 1 || destination.Meetings[0].Snapshot.Members[1].Name != "Bob" {
		t.Fatal("membership removal failed to prune current assignments or changed history")
	}
	admin.request("PUT", "/api/users/"+root.ID, userInput{Name: root.Name, Username: root.Username, Role: "member"}, 400, nil)
	member.request("POST", "/api/password", map[string]string{"current": "bob-password-123", "password": "new-password-456"}, 200, nil)
	other := testClient{t: t, handler: a.routes()}
	other.request("POST", "/api/login", map[string]string{"username": "bob", "password": "bob-password-123"}, 401, nil)
	other.request("POST", "/api/login", map[string]string{"username": "bob", "password": "new-password-456"}, 200, nil)
	admin.request("PUT", "/api/users/"+bob.ID, userInput{Name: "Robert", Username: "bob", Role: "member", Disabled: true}, 200, nil)
	member.request("GET", "/api/teams", nil, 401, nil)
	other.request("GET", "/api/teams", nil, 401, nil)
	reopened, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	saved, ok := reopened.get(destination.ID)
	if !ok || len(saved.Meetings) != 1 || len(saved.Metrics) != 1 {
		t.Fatal("data not persisted")
	}
	var backup Database
	admin.request("GET", "/api/export", nil, 200, &backup)
	admin.request("POST", "/api/import", map[string]any{"version": backup.Version, "database": backup}, 200, nil)
	admin.request("GET", "/api/teams", nil, 401, nil)
}

func TestCrossOriginAndStaticAssets(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "workspace.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := newApplication(s).routes()
	r := httptest.NewRequest("POST", "http://localhost/api/login", bytes.NewBufferString(`{}`))
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin mutation accepted")
	}
	for _, path := range []string{"/", "/app.js", "/styles.css", "/healthz"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("asset %s: %d", path, w.Code)
		}
	}
}
