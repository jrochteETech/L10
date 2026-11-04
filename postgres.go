package main

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed *.sql
var migrations embed.FS

type store struct {
	pool *sql.DB
}

func openStore(databaseURL string) (*store, error) {
	if databaseURL == "" && os.Getenv("PGHOST") == "" {
		return nil, fmt.Errorf("configure DATABASE_URL or PGHOST/PGDATABASE/PGUSER/PGPASSWORD for PostgreSQL")
	}
	pool, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	pool.SetMaxOpenConns(8)
	pool.SetMaxIdleConns(4)
	pool.SetConnMaxLifetime(30 * time.Minute)
	s := &store{pool: pool}
	if err := s.migrate(); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *store) close() error { return s.pool.Close() }

func (s *store) migrate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("connect PostgreSQL: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(101001)"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY)"); err != nil {
		return err
	}
	files, err := migrations.ReadDir(".")
	if err != nil {
		return err
	}
	for _, file := range files {
		var applied bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)", file.Name()).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := migrations.ReadFile(file.Name())
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", file.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (name) VALUES ($1)", file.Name()); err != nil {
			return err
		}
	}
	var version int64
	if err := tx.QueryRowContext(ctx, "SELECT version FROM workspace WHERE id = 1 FOR UPDATE").Scan(&version); err != nil {
		return err
	}
	if version == 0 {
		initial := Database{Version: 1, Users: []User{}, Teams: []State{newTeam("Leadership team")}}
		if err := saveDatabase(ctx, tx, Database{}, initial); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *store) snapshot() (Database, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Database{}, err
	}
	defer tx.Rollback()
	db, err := loadDatabase(ctx, tx)
	if err != nil {
		return Database{}, err
	}
	if err := tx.Commit(); err != nil {
		return Database{}, err
	}
	return db, nil
}

func (s *store) update(change func(*Database) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize mutations across connections before reading, so transfers and
	// version checks operate on the latest committed workspace.
	var version int64
	if err := tx.QueryRowContext(ctx, "SELECT version FROM workspace WHERE id = 1 FOR UPDATE").Scan(&version); err != nil {
		return err
	}
	before, err := loadDatabase(ctx, tx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(before)
	if err != nil {
		return err
	}
	var next Database
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	if err := change(&next); err != nil {
		return err
	}
	if err := validateDatabase(next); err != nil {
		return err
	}
	next.Version = version + 1
	if err := saveDatabase(ctx, tx, before, next); err != nil {
		return err
	}
	return tx.Commit()
}

func loadDatabase(ctx context.Context, tx *sql.Tx) (Database, error) {
	db := Database{Users: []User{}, Teams: []State{}}
	if err := tx.QueryRowContext(ctx, "SELECT version FROM workspace WHERE id = 1").Scan(&db.Version); err != nil {
		return db, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id, name, username, role, disabled, must_change_password, salt, password_hash FROM users ORDER BY position")
	if err != nil {
		return db, err
	}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Name, &u.Username, &u.Role, &u.Disabled, &u.MustChangePassword, &u.Salt, &u.PasswordHash); err != nil {
			rows.Close()
			return db, err
		}
		db.Users = append(db.Users, u)
	}
	if err := finishRows(rows); err != nil {
		return db, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT id, version, name, stage_minutes FROM teams ORDER BY position")
	if err != nil {
		return db, err
	}
	indices := map[string]int{}
	for rows.Next() {
		t := State{Members: []Member{}, Metrics: []Metric{}, Rocks: []Rock{}, Todos: []Todo{}, Issues: []Issue{}, Headlines: []Headline{}, Meetings: []Meeting{}}
		var minutes []byte
		if err := rows.Scan(&t.ID, &t.Version, &t.Name, &minutes); err != nil {
			rows.Close()
			return db, err
		}
		if err := json.Unmarshal(minutes, &t.StageMinutes); err != nil {
			rows.Close()
			return db, fmt.Errorf("read agenda for team %s: %w", t.ID, err)
		}
		indices[t.ID] = len(db.Teams)
		db.Teams = append(db.Teams, t)
	}
	if err := finishRows(rows); err != nil {
		return db, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT m.team_id, u.id, u.name FROM team_members m JOIN users u ON u.id = m.user_id ORDER BY m.team_id, m.position")
	if err != nil {
		return db, err
	}
	for rows.Next() {
		var teamID string
		var member Member
		if err := rows.Scan(&teamID, &member.ID, &member.Name); err != nil {
			rows.Close()
			return db, err
		}
		i := indices[teamID]
		db.Teams[i].Members = append(db.Teams[i].Members, member)
	}
	if err := finishRows(rows); err != nil {
		return db, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT team_id, kind, payload FROM team_items ORDER BY team_id, kind, position")
	if err != nil {
		return db, err
	}
	for rows.Next() {
		var teamID, kind string
		var payload []byte
		if err := rows.Scan(&teamID, &kind, &payload); err != nil {
			rows.Close()
			return db, err
		}
		t := &db.Teams[indices[teamID]]
		var err error
		switch kind {
		case "metrics":
			err = appendItem(&t.Metrics, payload)
		case "rocks":
			err = appendItem(&t.Rocks, payload)
		case "todos":
			err = appendItem(&t.Todos, payload)
		case "issues":
			err = appendItem(&t.Issues, payload)
		case "headlines":
			err = appendItem(&t.Headlines, payload)
		case "meetings":
			err = appendItem(&t.Meetings, payload)
		default:
			err = fmt.Errorf("unknown item kind %q", kind)
		}
		if err != nil {
			rows.Close()
			return db, fmt.Errorf("read %s in team %s: %w", kind, teamID, err)
		}
	}
	return db, finishRows(rows)
}

func finishRows(rows *sql.Rows) error {
	err := rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	return err
}

func appendItem[T any](items *[]T, payload []byte) error {
	var item T
	if err := json.Unmarshal(payload, &item); err != nil {
		return err
	}
	*items = append(*items, item)
	return nil
}

func saveDatabase(ctx context.Context, tx *sql.Tx, before, next Database) error {
	userIDs := make([]string, 0, len(next.Users))
	for i, u := range next.Users {
		userIDs = append(userIDs, u.ID)
		_, err := tx.ExecContext(ctx, `INSERT INTO users (id, position, name, username, role, disabled, must_change_password, salt, password_hash)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (id) DO UPDATE SET position=$2, name=$3, username=$4, role=$5, disabled=$6, must_change_password=$7, salt=$8, password_hash=$9`,
			u.ID, i, u.Name, u.Username, u.Role, u.Disabled, u.MustChangePassword, u.Salt, u.PasswordHash)
		if err != nil {
			return err
		}
	}
	teamIDs := make([]string, 0, len(next.Teams))
	for i, t := range next.Teams {
		teamIDs = append(teamIDs, t.ID)
		old, exists := findTeam(before, t.ID)
		oldJSON, err := json.Marshal(old)
		if err != nil {
			return err
		}
		newJSON, err := json.Marshal(t)
		if err != nil {
			return err
		}
		unchanged := exists && bytes.Equal(oldJSON, newJSON)
		minutes, err := json.Marshal(t.StageMinutes)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO teams (id, position, version, name, stage_minutes) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (id) DO UPDATE SET position=$2, version=$3, name=$4, stage_minutes=$5`, t.ID, i, t.Version, t.Name, minutes); err != nil {
			return err
		}
		if unchanged {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM team_members WHERE team_id = $1", t.ID); err != nil {
			return err
		}
		for position, member := range t.Members {
			if _, err := tx.ExecContext(ctx, "INSERT INTO team_members (team_id, user_id, position) VALUES ($1,$2,$3)", t.ID, member.ID, position); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM team_items WHERE team_id = $1", t.ID); err != nil {
			return err
		}
		groups := []struct {
			kind  string
			items any
		}{{"metrics", t.Metrics}, {"rocks", t.Rocks}, {"todos", t.Todos}, {"issues", t.Issues}, {"headlines", t.Headlines}, {"meetings", t.Meetings}}
		for _, group := range groups {
			data, err := json.Marshal(group.items)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO team_items (team_id, kind, id, position, payload)
				SELECT $1, $2, item->>'id', ordinal::integer, item
				FROM jsonb_array_elements(COALESCE(NULLIF($3::jsonb, 'null'::jsonb), '[]'::jsonb)) WITH ORDINALITY AS items(item, ordinal)`, t.ID, group.kind, data)
			if err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM teams WHERE NOT (id = ANY($1::text[]))", teamIDs); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM users WHERE NOT (id = ANY($1::text[]))", userIDs); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE workspace SET version = $1 WHERE id = 1", next.Version)
	return err
}
