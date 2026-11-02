package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func testStore(t *testing.T) (*store, string) {
	t.Helper()
	base := os.Getenv("L10_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("set L10_TEST_DATABASE_URL to run PostgreSQL integration tests (each test uses an isolated schema)")
	}
	pool, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	schema := "test_" + newID()
	if _, err := pool.Exec("CREATE SCHEMA " + schema); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Errorf("clean test schema: %v", err)
		}
		pool.Close()
	})
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("L10_TEST_DATABASE_URL must be a postgres:// or postgresql:// URL")
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	databaseURL := u.String()
	s, err := openStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.close() })
	return s, databaseURL
}

func backupFixture(t *testing.T) Database {
	t.Helper()
	salt, hash, err := hashPassword("migration-password")
	if err != nil {
		t.Fatal(err)
	}
	u := User{ID: "admin-id", Name: "Administrator", Username: "admin", Role: "admin", Salt: salt, PasswordHash: hash}
	team := newTeam("Imported team")
	team.ID = "team-id"
	team.Version = 12
	team.Members = []Member{{ID: u.ID, Name: u.Name}}
	team.Metrics = []Metric{{ID: "metric-id", Name: "Revenue", Owner: u.ID, Assignees: []string{u.ID}, Goal: 123.45, Direction: "gte", Values: map[string]float64{"2026-10-26": 456.78}}}
	team.Rocks = []Rock{{ID: "rock-id", Title: "Launch", Notes: "Preserve notes", Owner: u.ID, Status: "on-track", Assignees: []string{u.ID}}}
	team.Todos = []Todo{{ID: "todo-id", Title: "Follow up", Notes: "Keep details", Owner: u.ID, Assignees: []string{u.ID}}}
	team.Issues = []Issue{{ID: "issue-id", Title: "Capacity", Priority: 2, Status: "open", LongTerm: true, Assignees: []string{u.ID}}}
	team.Headlines = []Headline{{ID: "headline-id", Title: "Customer win", Kind: "customer", Assignees: []string{u.ID}}}
	team.Meetings = []Meeting{{
		ID: "meeting-id", StartedAt: "2026-10-26T14:00:00Z", EndedAt: "2026-10-26T15:00:00Z", Stage: 6,
		StageSeconds: []float64{300, 300, 300, 300, 300, 1800, 300}, Attendees: []string{u.ID},
		Ratings: map[string]float64{u.ID: 9.5}, ChosenIssueIDs: []string{"issue-id"},
		Snapshot: &Snapshot{Members: team.Members, Metrics: team.Metrics, Rocks: team.Rocks, Todos: team.Todos, Issues: team.Issues, Headlines: team.Headlines},
	}}
	db := Database{Version: 42, Users: []User{u}, Teams: []State{team}}
	if err := validateBackup(&db); err != nil {
		t.Fatal(err)
	}
	return db
}

func snapshotForTest(t *testing.T, s *store) Database {
	t.Helper()
	db, err := s.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPostgresImportRoundTripAndReplacement(t *testing.T) {
	s, databaseURL := testStore(t)
	want := backupFixture(t)
	if err := s.importDatabase(want, false); err != nil {
		t.Fatal(err)
	}
	other, err := openStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer other.close()
	got := snapshotForTest(t, other)
	want.Version = got.Version
	if !reflect.DeepEqual(got, want) {
		t.Fatal("import/reopen did not preserve backup data")
	}
	if !checkPassword(got.Users[0], "migration-password") {
		t.Fatal("import changed password credentials")
	}
	if err := s.importDatabase(want, false); !errors.Is(err, errInvalid) {
		t.Fatalf("nonempty database import: %v", err)
	}
	replacement := backupFixture(t)
	replacement.Users[0].ID = "replacement-admin"
	replacement.Teams[0].ID = "replacement-team"
	replacement.Teams[0].Members = []Member{{ID: replacement.Users[0].ID, Name: replacement.Users[0].Name}}
	replacement.Teams[0].Metrics = []Metric{}
	replacement.Teams[0].Rocks = []Rock{}
	replacement.Teams[0].Todos = []Todo{}
	replacement.Teams[0].Issues = []Issue{}
	replacement.Teams[0].Headlines = []Headline{}
	replacement.Teams[0].Meetings = []Meeting{}
	if err := s.importDatabase(replacement, true); err != nil {
		t.Fatal(err)
	}
	got = snapshotForTest(t, other)
	replacement.Version = got.Version
	if !reflect.DeepEqual(got, replacement) {
		t.Fatal("replacement retained old data or lost imported data")
	}
	invalid := replacement
	invalid.Users = append([]User(nil), replacement.Users...)
	invalid.Users[0].PasswordHash = "invalid"
	if err := s.importDatabase(invalid, true); err == nil {
		t.Fatal("invalid import accepted")
	}
	if !reflect.DeepEqual(snapshotForTest(t, other), got) {
		t.Fatal("invalid import changed database")
	}
}

func TestPostgresRollbackAndConcurrentVersions(t *testing.T) {
	s, databaseURL := testStore(t)
	before := snapshotForTest(t, s)
	if _, err := s.pool.Exec("ALTER TABLE teams ADD CONSTRAINT test_reject_name CHECK (name <> 'Rejected')"); err != nil {
		t.Fatal(err)
	}
	err := s.update(func(db *Database) error {
		db.Users = backupFixture(t).Users
		db.Teams[0].Name = "Rejected"
		return nil
	})
	if err == nil {
		t.Fatal("expected SQL constraint error")
	}
	if !reflect.DeepEqual(snapshotForTest(t, s), before) {
		t.Fatal("failed SQL transaction partially committed users, team, or version")
	}
	other, err := openStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer other.close()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, connection := range []*store{s, other} {
		wg.Add(1)
		go func(connection *store) {
			defer wg.Done()
			results <- connection.update(func(db *Database) error {
				if db.Teams[0].Version != before.Teams[0].Version {
					return errConflict
				}
				db.Teams[0].Name = "Concurrent update"
				db.Teams[0].Version++
				return nil
			})
		}(connection)
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, errConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d; want one of each", successes, conflicts)
	}
	after := snapshotForTest(t, other)
	if after.Version != before.Version+1 || after.Teams[0].Version != before.Teams[0].Version+1 {
		t.Fatal("failed transaction or conflict changed versions")
	}
}

func TestPostgresUnavailableReturnsErrors(t *testing.T) {
	s, _ := testStore(t)
	h := newApplication(s).routes()
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/healthz", "/api/session"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: got %d, want 503", path, w.Code)
		}
	}
	if err := s.update(func(*Database) error { return nil }); err == nil {
		t.Fatal("database failure was hidden")
	}
}

func TestPostgresRestoreSwapsUsernames(t *testing.T) {
	s, _ := testStore(t)
	db := backupFixture(t)
	member := db.Users[0]
	member.ID = "member-id"
	member.Username = "member"
	member.Role = "member"
	db.Users = append(db.Users, member)
	if err := s.importDatabase(db, false); err != nil {
		t.Fatal(err)
	}
	db.Users[0].Username, db.Users[1].Username = db.Users[1].Username, db.Users[0].Username
	if err := s.importDatabase(db, true); err != nil {
		t.Fatal(err)
	}
	got := snapshotForTest(t, s)
	db.Version = got.Version
	if !reflect.DeepEqual(got, db) {
		t.Fatal("restore lost accounts when swapping usernames")
	}
}

func TestReadBackupAndDryRun(t *testing.T) {
	db := backupFixture(t)
	db.Teams[0].StageMinutes = nil
	data, err := json.Marshal(db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readBackup(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Teams[0].StageMinutes, defaultStageMinutes()) {
		t.Fatal("legacy backup did not receive default agenda times")
	}
	for _, data := range []string{`{}`, `{"users":[],"teams":[]}`, string(data) + `{}`, strings.Replace(string(data), `"version":42`, `"unknown":true,"version":42`, 1)} {
		if _, err := readBackup(strings.NewReader(data)); err == nil {
			t.Fatal("malformed or invalid backup accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "backup.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", "postgres://unreachable.invalid/l10")
	var output bytes.Buffer
	if err := runCommand([]string{"import", "--dry-run", path}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "1 users, 1 teams, 6 work items/reports") {
		t.Fatalf("unexpected dry run summary: %s", output.String())
	}
}

func TestPostgresMigrationsAreIdempotent(t *testing.T) {
	s, _ := testStore(t)
	before := snapshotForTest(t, s)
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshotForTest(t, s), before) {
		t.Fatal("migration changed existing data")
	}
	var count int
	if err := s.pool.QueryRowContext(context.Background(), "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration count=%d, error=%v", count, err)
	}
}
