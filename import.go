package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func validateBackup(db *Database) error {
	if len(db.Users) == 0 || len(db.Teams) == 0 {
		return fmtInvalid("backup must include users and teams")
	}
	for i := range db.Teams {
		if len(db.Teams[i].StageMinutes) == 0 {
			db.Teams[i].StageMinutes = defaultStageMinutes()
		}
	}
	return validateDatabase(*db)
}

func readBackup(reader io.Reader) (Database, error) {
	var db Database
	d := json.NewDecoder(reader)
	d.DisallowUnknownFields()
	if err := d.Decode(&db); err != nil {
		return db, fmt.Errorf("decode backup: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return db, errors.New("backup must contain exactly one JSON object")
	}
	if err := validateBackup(&db); err != nil {
		return db, fmt.Errorf("validate backup: %w", err)
	}
	return db, nil
}

func (s *store) importDatabase(backup Database, replace bool) error {
	if err := validateBackup(&backup); err != nil {
		return err
	}
	return s.update(func(db *Database) error {
		if !replace && !isFreshDatabase(*db) {
			return fmtInvalid("database is not a fresh installation; stop the app and use --replace to overwrite it")
		}
		*db = backup
		return nil
	})
}

func isFreshDatabase(db Database) bool {
	if db.Version != 1 || len(db.Users) != 0 || len(db.Teams) != 1 {
		return false
	}
	t := db.Teams[0]
	return t.Version == 1 && len(t.Members)+len(t.Metrics)+len(t.Rocks)+len(t.Todos)+len(t.Issues)+len(t.Headlines)+len(t.Meetings) == 0
}

func runCommand(args []string, output io.Writer) error {
	if args[0] != "import" {
		return fmt.Errorf("unknown command %q; usage: l10 import [--dry-run] [--replace] BACKUP.json", args[0])
	}
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	flags.SetOutput(output)
	dryRun := flags.Bool("dry-run", false, "validate the backup without connecting to PostgreSQL")
	replace := flags.Bool("replace", false, "replace an existing database (stop the app first)")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: l10 import [--dry-run] [--replace] BACKUP.json")
	}
	file, err := os.Open(flags.Arg(0))
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	backup, readErr := readBackup(file)
	closeErr := file.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	items := 0
	for _, t := range backup.Teams {
		items += len(t.Metrics) + len(t.Rocks) + len(t.Todos) + len(t.Issues) + len(t.Headlines) + len(t.Meetings)
	}
	if *dryRun {
		_, err := fmt.Fprintf(output, "Backup valid: %d users, %d teams, %d work items/reports. No database changes made.\n", len(backup.Users), len(backup.Teams), items)
		return err
	}
	s, err := openStore(os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer s.close()
	if err := s.importDatabase(backup, *replace); err != nil {
		return fmt.Errorf("import failed (no backup data committed): %w", err)
	}
	_, err = fmt.Fprintf(output, "Import committed: %d users, %d teams, %d work items/reports. Sign in with an account from the backup.\n", len(backup.Users), len(backup.Teams), items)
	return err
}
