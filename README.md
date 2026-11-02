# Level Ten

A self-hosted EOS Level 10 meeting workspace. Go backend; vanilla JavaScript, HTML, and CSS frontend. The Compose stack includes the app and PostgreSQL; no frontend build step.

## Features

- Multiple teams with independent scorecards, quarterly rocks, to-dos, headlines, Issues Lists, and meeting history.
- Individual username/password accounts; users can belong to multiple teams.
- Administrators create users and teams, manage memberships, reset passwords, and disable accounts. New users receive the temporary password `password!!` and must replace it at first sign-in. Members can collaborate in their assigned teams. Administrators can access all teams.
- Multiple assigned users on every measurable, rock, to-do, issue, and headline. An optional primary owner provides accountability.
- Notes on rocks and to-dos, plus a long-term classification for issues. Use **Add to-do** on an issue to create actions without solving it (repeat for multiple to-dos), and the adjacent **Long term** toggle to change its classification.
- Move any individual item or concluded meeting report between accessible teams. Move all items from Team & settings. Transfers preserve assignments and add missing assignees to the destination team. Memberships in the source team stay intact.
- Standard 90-minute L10 agenda: Segue (5), Scorecard (5), Rocks (5), Headlines (5), To-dos (5), IDS (60), Conclude (5).
- The meeting’s Headlines step shows only headlines added today; the Headlines page retains the full list.
- Persistent section timer with pause/resume; prioritize issues, capture discussion notes, record resolutions, and create actions.
- Attendance check-in during Segue, cascading messages, individual 1–10 ratings, printable reports, and immutable historical workspace snapshots.
- Full JSON backup/restore, optimistic concurrency checking, transactional PostgreSQL persistence, and database-aware health checks.

## Run with Docker

Set `POSTGRES_PASSWORD` to a strong database password in your shell or your deployment platform's protected stack environment before running Compose. Use the same value on subsequent deployments; changing this variable does not change the password of an already initialized PostgreSQL volume. Never commit passwords.

```sh
docker compose up -d --build
```

Open **http://YOUR_SERVER:8111**. This uses the production theme and the `l10_postgres` database volume. For a separate local development instance, use:

```sh
docker compose -f compose.dev.yaml up -d --build
```

The development stack uses a prominent development banner, port **8110**, and its own `l10-dev_postgres` volume. The app defaults to production mode outside these Compose configurations. PostgreSQL is available only within each stack's Docker network; no database host port is published.

On a fresh installation, create the first administrator in the browser. For unattended bootstrap, supply `ADMIN_USERNAME`, `ADMIN_NAME`, and `ADMIN_PASSWORD` through Komodo's protected stack environment; the password must contain at least 10 characters. There is intentionally no default bootstrap password. Bootstrap variables apply only when no accounts exist. Never commit passwords or other secrets in env files.

Data is stored in PostgreSQL 17 in the `l10_postgres` named volume. Container upgrades preserve it. Do not use `docker compose down -v` unless you intend to delete the database. Schema migrations are embedded in the app and applied transactionally before it starts. PostgreSQL major-version upgrades require a separate database upgrade procedure; do not simply change the image's major version against an existing volume.

Use Team & settings → Back up & restore to export all teams and accounts. JSON exports retain the previous format and include password hashes; keep them private. Accounts, teams, and memberships use relational tables. Individual work items use separate rows with JSONB payloads to preserve nested fields and immutable meeting snapshots, rather than a single workspace document.

### Migrate an existing JSON installation

The app no longer reads or writes `workspace.json`. There is no automatic import on startup.

1. In the **old installation**, download a full JSON backup using Team & settings → Back up & restore. Pause edits, take a final export, and stop the old app. Keep the backup and old data volume.
2. Set `POSTGRES_PASSWORD` in the protected stack environment, build the new app, and start only PostgreSQL. Do not start the app or bootstrap an administrator before the initial import.

   ```sh
   docker compose build l10
   docker compose up -d postgres
   ```

3. Place the export at `./l10-backup.json` on the deployment host. Validate it without connecting to or changing the database:

   ```sh
   docker compose run --rm --no-deps --volume "./l10-backup.json:/backup.json:ro" l10 import --dry-run /backup.json
   ```

4. Import it in one transaction:

   ```sh
   docker compose run --rm --no-deps --volume "./l10-backup.json:/backup.json:ro" l10 import /backup.json
   docker compose up -d l10
   ```

5. Sign in with an account from the export. Verify teams, assignments, meeting history, and a fresh JSON export; restart the stack and verify persistence before retiring the old installation. Remove the host-side migration file or move it to private backup storage.

The CLI validates every account, membership, item, and report and prints counts. It preserves IDs, password hashes, team versions, array order, and historical snapshots. Missing agenda times in older exports receive the original defaults. The destination's overall workspace revision remains locally managed. Invalid backups and failed imports commit no backup data. First connection may initialize the schema and default empty team.

By default, import only accepts a fresh installation. If an app has already been used or an account bootstrapped, first export its data, stop the app (`docker compose stop l10`), and explicitly use `import --replace /backup.json`. **Replacement removes all existing accounts, teams, and meeting history.** Keep the app stopped throughout CLI imports so existing in-memory sessions cannot survive a replacement. For an online restore, use the administrator-only browser restore, which checks the workspace revision and invalidates sessions.

For development, substitute `docker compose -f compose.dev.yaml`, service `l10-dev`, and database service `postgres-dev` in the commands above. On Komodo, run the commands from the synced stack directory, with its stack environment and the same Compose project name as the deployed stack.

### Komodo deployment

1. Add this repository as a Komodo Repo resource and sync it onto the build/deployment server.
2. Create separate Komodo Stacks if you want both environments running. Point production at the `main` branch and `compose.yaml`; point development at your development branch and `compose.dev.yaml`.
3. Keep the stack names distinct so Docker Compose scopes the `l10_postgres` and `l10-dev_postgres` volumes independently. The files default to image tags `l10:latest` and `l10-dev:latest`, and host ports **8111** and **8110**; set `IMAGE_NAME` or `PORT` in the relevant stack environment to override them.
4. Set `POSTGRES_PASSWORD` in each protected stack environment. Optionally configure `ADMIN_*` for unattended first-account creation on a new installation; skip bootstrap when migrating a JSON backup. Do not put credentials in committed env files.
5. Deploy/build the stacks and visit the corresponding host port.

For a Komodo Deployment resource instead of a Stack, provision PostgreSQL separately, build the repository's `Dockerfile` as `l10:latest`, map host **8111** to container **8111**, and configure `DATABASE_URL` or `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD`, and `PGSSLMODE`. The app container runs as UID/GID `10001` and no longer needs a data volume. Require certificate-verified TLS (`sslmode=verify-full` with a trusted CA) when connecting outside the private Docker network.

Put a reverse proxy in front of the app for HTTPS when accessing it over the internet. Cookies become secure when requests use TLS or the proxy supplies `X-Forwarded-Proto: https`.

## First steps

1. Sign in as an administrator and create accounts in **Team & settings → User management**. Give each new user the temporary password `password!!`; the app requires them to choose a new password at first sign-in.
2. Create teams and assign users using **Manage membership**. Users may belong to several teams.
3. Add measurables, rocks, issues, headlines, and to-dos; select a primary owner and any additional assignees.
4. Start an L10, follow the agenda, solve issues, and conclude to save a report.
5. Switch teams with the sidebar selector. Use **⇄** to move individual items, or **Move all items** for a bulk transfer. Conclude any active meeting before moving its report or doing a full transfer.

Removing team membership clears that user's current assignments in that team. Historical reports preserve the original names and assignments. Disabling an account prevents login while preserving records. At least one active administrator must remain.

## Local development

Requires Go 1.24 or newer and PostgreSQL 17. Configure `DATABASE_URL` (a PostgreSQL connection URL/DSN) or the `PG*` variables listed above. Compose supplies the `PG*` variables automatically.

```sh
go run .
go test ./...
go vet ./...
```

Default HTTP port: `8111`, overridden using `PORT`. `DATA_DIR` is no longer used. Frontend assets are embedded into the Go executable, so restart after changing frontend files.

Database-backed tests are skipped unless `L10_TEST_DATABASE_URL` is set to a `postgres://` or `postgresql://` URL. Set it to a disposable PostgreSQL database and run `go test ./...` to exercise the API, imports, migrations, rollback, persistence, and concurrent edits. The test account must be able to create schemas; each integration test creates and removes an isolated schema. Pure backup validation and model validation tests run without PostgreSQL.

Outside Docker, use `go run . import --dry-run BACKUP.json` or `go run . import BACKUP.json`. Flags must precede the filename.

## Operational notes

- Run one app instance per database while sessions and login rate limits remain in memory. PostgreSQL serializes writes and enforces conflict checks across connections; reads use a consistent transactional snapshot.
- Changes save to the server; notes have explicit Save buttons and also save on agenda transitions. Use Refresh to load changes made in another browser; concurrent edits return a conflict rather than silently overwrite data.
- Sessions are in memory and expire after seven days. Server restarts require a new login. Changing passwords or editing accounts invalidates their existing sessions. Setting a user's temporary password requires them to replace it at their next sign-in.
- Passwords are salted and hashed with PBKDF2-HMAC-SHA256 (600,000 iterations). Login/setup/password attempts are rate-limited.
- Browser backup restore replaces all teams and accounts, invalidates sessions, and requires login with a restored account. Export a current backup first. Browser request bodies are limited to 8 MiB; use the offline CLI for larger exports.
- The app checks PostgreSQL connectivity at `/healthz`, returning HTTP 503 when unavailable. API database-read failures are also reported explicitly.
- This is an independent implementation of the EOS Level 10 meeting workflow, not an official EOS product.
