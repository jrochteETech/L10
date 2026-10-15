package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Member struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type User struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Username           string `json:"username"`
	Role               string `json:"role"`
	Disabled           bool   `json:"disabled"`
	MustChangePassword bool   `json:"mustChangePassword"`
	Salt               string `json:"salt,omitempty"`
	PasswordHash       string `json:"passwordHash,omitempty"`
}
type Database struct {
	Version int64   `json:"version"`
	Users   []User  `json:"users"`
	Teams   []State `json:"teams"`
}
type Metric struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Owner     string             `json:"owner"`
	Goal      float64            `json:"goal"`
	Direction string             `json:"direction"`
	Unit      string             `json:"unit"`
	Values    map[string]float64 `json:"values"`
	Assignees []string           `json:"assignees"`
}
type Rock struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Notes     string   `json:"notes"`
	Owner     string   `json:"owner"`
	Assignees []string `json:"assignees"`
	Due       string   `json:"due"`
	Status    string   `json:"status"`
}
type Todo struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Notes       string   `json:"notes"`
	Owner       string   `json:"owner"`
	Assignees   []string `json:"assignees"`
	Due         string   `json:"due"`
	Done        bool     `json:"done"`
	CompletedAt string   `json:"completedAt"`
}
type Issue struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Details   string   `json:"details"`
	LongTerm  bool     `json:"longTerm"`
	Owner     string   `json:"owner"`
	Assignees []string `json:"assignees"`
	Priority  int      `json:"priority"`
	Status    string   `json:"status"`
	CreatedAt string   `json:"createdAt"`
}
type Headline struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Kind      string   `json:"kind"`
	Assignees []string `json:"assignees"`
	CreatedAt string   `json:"createdAt"`
}
type Snapshot struct {
	Members   []Member   `json:"members"`
	Metrics   []Metric   `json:"metrics"`
	Rocks     []Rock     `json:"rocks"`
	Todos     []Todo     `json:"todos"`
	Issues    []Issue    `json:"issues"`
	Headlines []Headline `json:"headlines"`
}
type Meeting struct {
	ID             string             `json:"id"`
	StartedAt      string             `json:"startedAt"`
	EndedAt        string             `json:"endedAt"`
	Stage          int                `json:"stage"`
	StageSeconds   []float64          `json:"stageSeconds"`
	TimerStartedAt string             `json:"timerStartedAt"`
	ChosenIssueIDs []string           `json:"chosenIssueIds"`
	Attendees      []string           `json:"attendees"`
	Segue          string             `json:"segue"`
	Notes          string             `json:"notes"`
	Messages       string             `json:"messages"`
	Ratings        map[string]float64 `json:"ratings"`
	Snapshot       *Snapshot          `json:"snapshot"`
}
type State struct {
	Version      int64      `json:"version"`
	Name         string     `json:"name"`
	ID           string     `json:"id"`
	StageMinutes []int      `json:"stageMinutes"`
	Members      []Member   `json:"members"`
	Metrics      []Metric   `json:"metrics"`
	Rocks        []Rock     `json:"rocks"`
	Todos        []Todo     `json:"todos"`
	Issues       []Issue    `json:"issues"`
	Headlines    []Headline `json:"headlines"`
	Meetings     []Meeting  `json:"meetings"`
}

var errConflict = errors.New("workspace version conflict")
var errInvalid = errors.New("invalid workspace")

const defaultUserPassword = "password!!"

type store struct {
	mu   sync.RWMutex
	path string
	db   Database
}

func defaultStageMinutes() []int { return []int{5, 5, 5, 5, 5, 60, 5} }

func newTeam(name string) State {
	return State{ID: newID(), Version: 1, Name: name, StageMinutes: defaultStageMinutes(), Members: []Member{}, Metrics: []Metric{}, Rocks: []Rock{}, Todos: []Todo{}, Issues: []Issue{}, Headlines: []Headline{}, Meetings: []Meeting{}}
}
func newID() string {
	var b [16]byte
	_, err := rand.Read(b[:])
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func hashPassword(password string) (string, string, error) {
	if len(password) < 10 || len(password) > 256 {
		return "", "", fmt.Errorf("password must be between 10 and 256 characters")
	}
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return "", "", err
	}
	hash, err := pbkdf2.Key(sha256.New, password, salt[:], 600000, 32)
	return hex.EncodeToString(salt[:]), hex.EncodeToString(hash), err
}
func checkPassword(user User, password string) bool {
	if len(password) > 256 {
		return false
	}
	salt, err := hex.DecodeString(user.Salt)
	if err != nil || len(salt) != 16 {
		salt = make([]byte, 16)
	}
	hash, err := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	if err != nil {
		return false
	}
	expected, _ := hex.DecodeString(user.PasswordHash)
	return len(expected) == 32 && subtle.ConstantTimeCompare(expected, hash) == 1
}
func publicUser(user User) User { user.Salt = ""; user.PasswordHash = ""; return user }

func openStore(path string) (*store, error) {
	s := &store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.db = Database{Version: 1, Users: []User{}, Teams: []State{newTeam("Leadership team")}}
		if err := s.persist(s.db); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.db); err != nil {
		return nil, fmt.Errorf("read workspace: %w", err)
	}
	if s.db.Teams == nil {
		return nil, errors.New("data file is not a multi-team database")
	}
	for i := range s.db.Teams {
		if len(s.db.Teams[i].StageMinutes) == 0 {
			s.db.Teams[i].StageMinutes = defaultStageMinutes()
		}
	}
	if err := validateDatabase(s.db); err != nil {
		return nil, fmt.Errorf("read workspace: %w", err)
	}
	return s, nil
}

func clone(s State) State {
	data, _ := json.Marshal(s)
	var result State
	_ = json.Unmarshal(data, &result)
	return result
}
func (s *store) get(teamID string) (State, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, team := range s.db.Teams {
		if team.ID == teamID {
			return clone(team), true
		}
	}
	return State{}, false
}
func (s *store) update(change func(*Database) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, _ := json.Marshal(s.db)
	var next Database
	_ = json.Unmarshal(data, &next)
	if err := change(&next); err != nil {
		return err
	}
	if err := validateDatabase(next); err != nil {
		return err
	}
	next.Version++
	if err := s.persist(next); err != nil {
		return err
	}
	s.db = next
	return nil
}

func (s *store) persist(next Database) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".workspace-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

func validateDatabase(db Database) error {
	if len(db.Users) > 1000 || len(db.Teams) > 100 {
		return fmt.Errorf("%w: too many users or teams", errInvalid)
	}
	users := map[string]User{}
	names := map[string]bool{}
	admins := 0
	for _, u := range db.Users {
		if u.ID == "" || users[u.ID].ID != "" || strings.TrimSpace(u.Name) == "" || len(u.Name) > 100 || !validUsername(u.Username) || names[u.Username] || (u.Role != "admin" && u.Role != "member") {
			return fmt.Errorf("%w: invalid or duplicate user", errInvalid)
		}
		salt, e1 := hex.DecodeString(u.Salt)
		hash, e2 := hex.DecodeString(u.PasswordHash)
		if e1 != nil || e2 != nil || len(salt) != 16 || len(hash) != 32 {
			return fmt.Errorf("%w: invalid password hash", errInvalid)
		}
		users[u.ID] = u
		names[u.Username] = true
		if u.Role == "admin" && !u.Disabled {
			admins++
		}
	}
	if len(db.Users) > 0 && admins == 0 {
		return fmt.Errorf("%w: at least one active administrator is required", errInvalid)
	}
	teams := map[string]bool{}
	for _, t := range db.Teams {
		if t.ID == "" || teams[t.ID] {
			return fmt.Errorf("%w: invalid team ID", errInvalid)
		}
		teams[t.ID] = true
		for _, m := range t.Members {
			if u, ok := users[m.ID]; !ok || u.Name != m.Name {
				return fmt.Errorf("%w: team member must be an existing user", errInvalid)
			}
		}
		if err := validate(t); err != nil {
			return err
		}
	}
	return nil
}
func validUsername(name string) bool {
	if len(name) < 3 || len(name) > 80 || name != strings.ToLower(name) {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._-@", r)) {
			return false
		}
	}
	return true
}

func validate(s State) error {
	bad := func(message string) error { return fmt.Errorf("%w: %s", errInvalid, message) }
	if strings.TrimSpace(s.Name) == "" || len(s.Name) > 200 {
		return bad("workspace name is required (maximum 200 characters)")
	}
	if len(s.StageMinutes) != 0 && len(s.StageMinutes) != 7 {
		return bad("meeting agenda must have seven section times")
	}
	for _, minutes := range s.StageMinutes {
		if minutes < 1 || minutes > 180 {
			return bad("meeting section times must be between 1 and 180 minutes")
		}
	}
	if len(s.Members) > 100 || len(s.Metrics) > 300 || len(s.Rocks) > 1000 || len(s.Todos) > 10000 || len(s.Issues) > 10000 || len(s.Headlines) > 10000 || len(s.Meetings) > 2000 {
		return bad("workspace item limit exceeded")
	}
	ids := map[string]bool{}
	idOK := func(id string) bool {
		if id == "" || len(id) > 100 || ids[id] {
			return false
		}
		ids[id] = true
		return true
	}
	members := map[string]bool{}
	for _, m := range s.Members {
		if !idOK(m.ID) || strings.TrimSpace(m.Name) == "" || len(m.Name) > 100 {
			return bad("invalid member")
		}
		members[m.ID] = true
	}
	ownerOK := func(owner string) bool { return owner == "" || members[owner] }
	assigneesOK := func(ids []string) bool {
		seen := map[string]bool{}
		for _, id := range ids {
			if !members[id] || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	}
	textOK := func(text string) bool { return strings.TrimSpace(text) != "" && len(text) <= 500 }
	dateOK := func(value string) bool {
		if value == "" {
			return true
		}
		_, err := time.Parse("2006-01-02", value)
		return err == nil
	}
	timeOK := func(value string) bool {
		if value == "" {
			return true
		}
		_, err := time.Parse(time.RFC3339Nano, value)
		return err == nil
	}
	for _, m := range s.Metrics {
		if !idOK(m.ID) || !textOK(m.Name) || !ownerOK(m.Owner) || !assigneesOK(m.Assignees) || (m.Direction != "gte" && m.Direction != "lte") || len(m.Unit) > 30 || math.IsNaN(m.Goal) || math.IsInf(m.Goal, 0) {
			return bad("invalid scorecard measurable")
		}
		for week, value := range m.Values {
			if week == "" || !dateOK(week) || math.IsNaN(value) || math.IsInf(value, 0) {
				return bad("invalid scorecard value")
			}
		}
	}
	for _, r := range s.Rocks {
		if !idOK(r.ID) || !textOK(r.Title) || len(r.Notes) > 20000 || !ownerOK(r.Owner) || !assigneesOK(r.Assignees) || !dateOK(r.Due) || (r.Status != "on-track" && r.Status != "off-track" && r.Status != "done") {
			return bad("invalid rock")
		}
	}
	for _, t := range s.Todos {
		if !idOK(t.ID) || !textOK(t.Title) || len(t.Notes) > 20000 || !ownerOK(t.Owner) || !assigneesOK(t.Assignees) || !dateOK(t.Due) || !timeOK(t.CompletedAt) {
			return bad("invalid to-do")
		}
	}
	issues := map[string]Issue{}
	for _, i := range s.Issues {
		if !idOK(i.ID) || !textOK(i.Title) || !ownerOK(i.Owner) || !assigneesOK(i.Assignees) || len(i.Details) > 20000 || i.Priority < 1 || i.Priority > 3 || (i.Status != "open" && i.Status != "solved") || !timeOK(i.CreatedAt) {
			return bad("invalid issue")
		}
		issues[i.ID] = i
	}
	for _, h := range s.Headlines {
		if !idOK(h.ID) || !textOK(h.Title) || !assigneesOK(h.Assignees) || (h.Kind != "customer" && h.Kind != "employee") || !timeOK(h.CreatedAt) {
			return bad("invalid headline")
		}
	}
	active := 0
	for _, m := range s.Meetings {
		if !idOK(m.ID) || m.StartedAt == "" || !timeOK(m.StartedAt) || !timeOK(m.EndedAt) || !timeOK(m.TimerStartedAt) || m.Stage < 0 || m.Stage > 6 || len(m.StageSeconds) != 7 || len(m.Segue) > 20000 || len(m.Notes) > 50000 || len(m.Messages) > 20000 {
			return bad("invalid meeting")
		}
		if m.EndedAt == "" {
			active++
			if m.Snapshot != nil {
				return bad("active meeting cannot have a snapshot")
			}
		} else {
			start, _ := time.Parse(time.RFC3339Nano, m.StartedAt)
			end, _ := time.Parse(time.RFC3339Nano, m.EndedAt)
			if end.Before(start) || m.TimerStartedAt != "" || m.Snapshot == nil {
				return bad("invalid concluded meeting")
			}
			ss := State{Name: s.Name, Members: m.Snapshot.Members, Metrics: m.Snapshot.Metrics, Rocks: m.Snapshot.Rocks, Todos: m.Snapshot.Todos, Issues: m.Snapshot.Issues, Headlines: m.Snapshot.Headlines}
			if err := validate(ss); err != nil {
				return bad("invalid meeting snapshot")
			}
		}
		meetingMembers := members
		if m.Snapshot != nil {
			meetingMembers = map[string]bool{}
			for _, member := range m.Snapshot.Members {
				meetingMembers[member.ID] = true
			}
		}
		attendees := map[string]bool{}
		for _, id := range m.Attendees {
			if !meetingMembers[id] || attendees[id] {
				return bad("invalid meeting attendee")
			}
			attendees[id] = true
		}
		for id, rating := range m.Ratings {
			if !attendees[id] || math.IsNaN(rating) || math.IsInf(rating, 0) || rating < 1 || rating > 10 || math.Abs(rating*10-math.Round(rating*10)) > 1e-9 {
				return bad("ratings must be from attending members, between 1 and 10, and use at most one decimal place")
			}
		}
		meetingIssues := issues
		requireOpen := true
		if m.Snapshot != nil {
			meetingIssues = map[string]Issue{}
			for _, issue := range m.Snapshot.Issues {
				meetingIssues[issue.ID] = issue
			}
			requireOpen = false
		}
		chosen := map[string]bool{}
		if len(m.ChosenIssueIDs) > 3 {
			return bad("a meeting can choose at most three issues")
		}
		for _, id := range m.ChosenIssueIDs {
			issue, exists := meetingIssues[id]
			if !exists || requireOpen && issue.Status != "open" || chosen[id] {
				return bad("chosen meeting issues must be unique open issues")
			}
			chosen[id] = true
		}
		for _, seconds := range m.StageSeconds {
			if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
				return bad("invalid meeting timer")
			}
		}
	}
	if active > 1 {
		return bad("only one meeting can be active")
	}
	return nil
}
