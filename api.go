package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (a *application) listTeams(w http.ResponseWriter, r *http.Request) {
	u, _ := a.currentUser(r)
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	result := []map[string]any{}
	for _, t := range a.store.db.Teams {
		if canAccess(u, t) {
			result = append(result, map[string]any{"id": t.ID, "name": t.Name, "stageMinutes": t.StageMinutes, "members": t.Members, "version": t.Version})
		}
	}
	writeJSON(w, 200, result)
}
func (a *application) createTeam(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name    string   `json:"name"`
		Members []string `json:"members"`
	}
	if err := decode(w, r, &input); err != nil {
		writeError(w, 400, "Invalid team")
		return
	}
	u, _ := a.currentUser(r)
	team := newTeam(strings.TrimSpace(input.Name))
	if len(input.Members) == 0 {
		input.Members = []string{u.ID}
	}
	err := a.store.update(func(db *Database) error {
		seen := map[string]bool{}
		for _, id := range input.Members {
			if seen[id] {
				continue
			}
			seen[id] = true
			found := false
			for _, user := range db.Users {
				if user.ID == id {
					team.Members = append(team.Members, Member{user.ID, user.Name})
					found = true
					break
				}
			}
			if !found {
				return fmtInvalid("unknown member")
			}
		}
		db.Teams = append(db.Teams, team)
		return nil
	})
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, 201, team)
}
func (a *application) getState(w http.ResponseWriter, r *http.Request) {
	u, _ := a.currentUser(r)
	t, ok := a.store.get(r.PathValue("team"))
	if !ok {
		respondError(w, errNotFound)
		return
	}
	if !canAccess(u, t) {
		respondError(w, errForbidden)
		return
	}
	writeJSON(w, 200, t)
}
func (a *application) saveState(w http.ResponseWriter, r *http.Request) {
	u, _ := a.currentUser(r)
	var next State
	if err := decode(w, r, &next); err != nil {
		writeError(w, 400, "Invalid workspace: "+err.Error())
		return
	}
	err := a.store.update(func(db *Database) error {
		for i, t := range db.Teams {
			if t.ID != r.PathValue("team") {
				continue
			}
			if !canAccess(u, t) {
				return errForbidden
			}
			if next.ID != t.ID {
				return fmtInvalid("team ID cannot change")
			}
			if next.Version != t.Version {
				return errConflict
			}
			// Membership is managed through the administrator-only endpoint.
			oldMembers, _ := json.Marshal(t.Members)
			newMembers, _ := json.Marshal(next.Members)
			if string(oldMembers) != string(newMembers) {
				return fmtInvalid("use team membership settings to change members")
			}
			for _, previous := range t.Meetings {
				if previous.EndedAt == "" {
					continue
				}
				unchanged := false
				for _, meeting := range next.Meetings {
					if meeting.ID == previous.ID {
						oldJSON, _ := json.Marshal(previous)
						newJSON, _ := json.Marshal(meeting)
						unchanged = string(oldJSON) == string(newJSON)
						break
					}
				}
				if !unchanged {
					return fmtInvalid("concluded reports cannot be edited or removed; use transfer to move them")
				}
			}
			next.Version++
			db.Teams[i] = next
			return nil
		}
		return errNotFound
	})
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, 200, next)
}
func (a *application) setMembers(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Members []string `json:"members"`
		Version int64    `json:"version"`
	}
	if err := decode(w, r, &input); err != nil {
		writeError(w, 400, "Invalid membership")
		return
	}
	err := a.store.update(func(db *Database) error {
		for i := range db.Teams {
			t := &db.Teams[i]
			if t.ID != r.PathValue("team") {
				continue
			}
			if t.Version != input.Version {
				return errConflict
			}
			members := []Member{}
			keep := map[string]bool{}
			for _, id := range input.Members {
				if keep[id] {
					continue
				}
				found := false
				for _, u := range db.Users {
					if u.ID == id {
						members = append(members, Member{u.ID, u.Name})
						keep[id] = true
						found = true
						break
					}
				}
				if !found {
					return fmtInvalid("unknown user")
				}
			}
			prune := func(ids []string) []string {
				out := []string{}
				for _, id := range ids {
					if keep[id] {
						out = append(out, id)
					}
				}
				return out
			}
			owner := func(id string) string {
				if keep[id] {
					return id
				}
				return ""
			}
			for j := range t.Metrics {
				t.Metrics[j].Owner = owner(t.Metrics[j].Owner)
				t.Metrics[j].Assignees = prune(t.Metrics[j].Assignees)
			}
			for j := range t.Rocks {
				t.Rocks[j].Owner = owner(t.Rocks[j].Owner)
				t.Rocks[j].Assignees = prune(t.Rocks[j].Assignees)
			}
			for j := range t.Todos {
				t.Todos[j].Owner = owner(t.Todos[j].Owner)
				t.Todos[j].Assignees = prune(t.Todos[j].Assignees)
			}
			for j := range t.Issues {
				t.Issues[j].Owner = owner(t.Issues[j].Owner)
				t.Issues[j].Assignees = prune(t.Issues[j].Assignees)
			}
			for j := range t.Headlines {
				t.Headlines[j].Assignees = prune(t.Headlines[j].Assignees)
			}
			for j := range t.Meetings {
				m := &t.Meetings[j]
				if m.EndedAt == "" {
					m.Attendees = prune(m.Attendees)
					for id := range m.Ratings {
						if !keep[id] {
							delete(m.Ratings, id)
						}
					}
				}
			}
			t.Members = members
			t.Version++
			return nil
		}
		return errNotFound
	})
	if err != nil {
		respondError(w, err)
		return
	}
	t, _ := a.store.get(r.PathValue("team"))
	writeJSON(w, 200, t)
}
func (a *application) listUsers(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	users := []User{}
	for _, u := range a.store.db.Users {
		users = append(users, publicUser(u))
	}
	writeJSON(w, 200, users)
}

type userInput struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

func (a *application) createUser(w http.ResponseWriter, r *http.Request) {
	var input userInput
	if err := decode(w, r, &input); err != nil {
		writeError(w, 400, "Invalid user")
		return
	}
	salt, hash, err := hashPassword(defaultUserPassword)
	if err != nil {
		respondError(w, err)
		return
	}
	u := User{ID: newID(), Name: strings.TrimSpace(input.Name), Username: strings.ToLower(strings.TrimSpace(input.Username)), Role: input.Role, Disabled: input.Disabled, MustChangePassword: true, Salt: salt, PasswordHash: hash}
	if err := a.store.update(func(db *Database) error { db.Users = append(db.Users, u); return nil }); err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, 201, publicUser(u))
}
func (a *application) updateUser(w http.ResponseWriter, r *http.Request) {
	var input userInput
	if err := decode(w, r, &input); err != nil {
		writeError(w, 400, "Invalid user")
		return
	}
	var salt, hash string
	var err error
	if input.Password != "" {
		salt, hash, err = hashPassword(input.Password)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	var result User
	err = a.store.update(func(db *Database) error {
		for i := range db.Users {
			u := &db.Users[i]
			if u.ID != r.PathValue("user") {
				continue
			}
			u.Name = strings.TrimSpace(input.Name)
			u.Username = strings.ToLower(strings.TrimSpace(input.Username))
			u.Role = input.Role
			u.Disabled = input.Disabled
			if hash != "" {
				u.Salt = salt
				u.PasswordHash = hash
				u.MustChangePassword = true
			}
			result = *u
			for j := range db.Teams {
				t := &db.Teams[j]
				changed := false
				for k := range t.Members {
					if t.Members[k].ID == u.ID {
						t.Members[k].Name = u.Name
						changed = true
					}
				}
				if changed {
					t.Version++
				}
			}
			return nil
		}
		return errNotFound
	})
	if err != nil {
		respondError(w, err)
		return
	}
	a.invalidate(result.ID)
	writeJSON(w, 200, publicUser(result))
}

type transferInput struct {
	Source             string              `json:"source"`
	Destination        string              `json:"destination"`
	Version            int64               `json:"version"`
	DestinationVersion int64               `json:"destinationVersion"`
	Items              map[string][]string `json:"items"`
	All                bool                `json:"all"`
}

func moveItems[T any](source *[]T, destination *[]T, ids []string, all bool, getID func(T) string) error {
	requested := map[string]bool{}
	for _, id := range ids {
		requested[id] = true
	}
	kept := []T{}
	existing := map[string]bool{}
	for _, item := range *destination {
		existing[getID(item)] = true
	}
	for _, item := range *source {
		id := getID(item)
		if all || requested[id] {
			if existing[id] {
				return fmtInvalid("destination already contains an item with this ID")
			}
			*destination = append(*destination, item)
			delete(requested, id)
		} else {
			kept = append(kept, item)
		}
	}
	if len(requested) > 0 {
		return errNotFound
	}
	*source = kept
	return nil
}
func (a *application) transfer(w http.ResponseWriter, r *http.Request) {
	var input transferInput
	if err := decode(w, r, &input); err != nil {
		writeError(w, 400, "Invalid transfer")
		return
	}
	u, _ := a.currentUser(r)
	err := a.store.update(func(db *Database) error {
		if input.Source == input.Destination {
			return fmtInvalid("choose a different destination team")
		}
		var source, destination *State
		for i := range db.Teams {
			if db.Teams[i].ID == input.Source {
				source = &db.Teams[i]
			}
			if db.Teams[i].ID == input.Destination {
				destination = &db.Teams[i]
			}
		}
		if source == nil || destination == nil {
			return errNotFound
		}
		if !canAccess(u, *source) || !canAccess(u, *destination) {
			return errForbidden
		}
		if source.Version != input.Version || destination.Version != input.DestinationVersion {
			return errConflict
		}
		for key := range input.Items {
			if key != "metrics" && key != "rocks" && key != "todos" && key != "issues" && key != "headlines" && key != "meetings" {
				return fmtInvalid("unknown item type")
			}
		}
		// Only concluded meetings can move; live timers stay with their team.
		for _, m := range source.Meetings {
			selected := input.All
			for _, id := range input.Items["meetings"] {
				if id == m.ID {
					selected = true
				}
			}
			if selected && m.EndedAt == "" {
				return fmtInvalid("conclude the active meeting before moving it or moving all items")
			}
		}
		if err := moveItems(&source.Metrics, &destination.Metrics, input.Items["metrics"], input.All, func(v Metric) string { return v.ID }); err != nil {
			return err
		}
		if err := moveItems(&source.Rocks, &destination.Rocks, input.Items["rocks"], input.All, func(v Rock) string { return v.ID }); err != nil {
			return err
		}
		if err := moveItems(&source.Todos, &destination.Todos, input.Items["todos"], input.All, func(v Todo) string { return v.ID }); err != nil {
			return err
		}
		if err := moveItems(&source.Issues, &destination.Issues, input.Items["issues"], input.All, func(v Issue) string { return v.ID }); err != nil {
			return err
		}
		if err := moveItems(&source.Headlines, &destination.Headlines, input.Items["headlines"], input.All, func(v Headline) string { return v.ID }); err != nil {
			return err
		}
		if err := moveItems(&source.Meetings, &destination.Meetings, input.Items["meetings"], input.All, func(v Meeting) string { return v.ID }); err != nil {
			return err
		}
		// Preserve primary owners and every assignee by adding missing destination memberships.
		needed := map[string]bool{}
		add := func(owner string, ids []string) {
			if owner != "" {
				needed[owner] = true
			}
			for _, id := range ids {
				needed[id] = true
			}
		}
		for _, m := range destination.Metrics {
			add(m.Owner, m.Assignees)
		}
		for _, v := range destination.Rocks {
			add(v.Owner, v.Assignees)
		}
		for _, v := range destination.Todos {
			add(v.Owner, v.Assignees)
		}
		for _, v := range destination.Issues {
			add(v.Owner, v.Assignees)
		}
		for _, v := range destination.Headlines {
			add("", v.Assignees)
		}
		for _, m := range destination.Members {
			delete(needed, m.ID)
		}
		for _, user := range db.Users {
			if needed[user.ID] {
				destination.Members = append(destination.Members, Member{user.ID, user.Name})
			}
		}
		source.Version++
		destination.Version++
		return nil
	})
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *application) export(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	w.Header().Set("Content-Disposition", `attachment; filename="l10-backup.json"`)
	writeJSON(w, 200, a.store.db)
}
func (a *application) importBackup(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Version  int64    `json:"version"`
		Database Database `json:"database"`
	}
	if err := decode(w, r, &input); err != nil {
		writeError(w, 400, "Invalid backup")
		return
	}
	err := a.store.update(func(db *Database) error {
		if db.Version != input.Version {
			return errConflict
		}
		if len(input.Database.Users) == 0 || len(input.Database.Teams) == 0 {
			return fmtInvalid("backup must include users and teams")
		}
		v := db.Version
		*db = input.Database
		db.Version = v
		return nil
	})
	if err != nil {
		respondError(w, err)
		return
	}
	a.mu.Lock()
	a.sessions = map[string]session{}
	a.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}
