# Level Ten

A self-hosted EOS Level 10 meeting workspace. Go backend; vanilla JavaScript, HTML, and CSS frontend. One container, no external database or frontend build step.

## Features

- Multiple teams with independent scorecards, quarterly rocks, to-dos, headlines, Issues Lists, and meeting history.
- Individual username/password accounts; users can belong to multiple teams.
- Administrators create users and teams, manage memberships, reset passwords, and disable accounts. New users receive the temporary password `password!!` and must replace it at first sign-in. Members can collaborate in their assigned teams. Administrators can access all teams.
- Multiple assigned users on every measurable, rock, to-do, issue, and headline. An optional primary owner provides accountability.
- Notes on rocks and to-dos, plus a long-term classification for issues.
- Move any individual item or concluded meeting report between accessible teams. Move all items from Team & settings. Transfers preserve assignments and add missing assignees to the destination team. Memberships in the source team stay intact.
- Standard 90-minute L10 agenda: Segue (5), Scorecard (5), Rocks (5), Headlines (5), To-dos (5), IDS (60), Conclude (5).
- Persistent section timer with pause/resume; prioritize issues, capture discussion notes, record resolutions, and create actions.
- Attendance check-in during Segue, cascading messages, individual 1–10 ratings, printable reports, and immutable historical workspace snapshots.
- Full JSON backup/restore, optimistic concurrency checking, atomic on-disk updates, and health checks.

## Run with Docker

```sh
docker compose up -d --build
```

Open **http://YOUR_SERVER:8111**. On a fresh installation, create the first administrator in the browser. Alternatively, configure `ADMIN_USERNAME`, `ADMIN_NAME`, and `ADMIN_PASSWORD` in Komodo or a `.env` file before the first start. The password must contain at least 10 characters. Bootstrap variables only apply when no accounts exist.

Data is stored in the `l10_data` named volume at `/data/workspace.json`. Container upgrades preserve it. Use Team & settings → Back up & restore to export all teams and accounts; backups contain password hashes and should be kept private.

### Komodo deployment

1. Add this repository as a Komodo Repo resource and sync it onto the build/deployment server.
2. Create a Stack using this repository and `compose.yaml` as the compose file.
3. Optionally configure the `ADMIN_*` variables in the stack environment for unattended initial account creation.
4. Deploy/build the stack and visit port **8111** on the server.

For a Komodo Deployment resource instead of a Stack, build the repository's `Dockerfile` as `l10:latest`, map host **8111** to container **8111**, and attach a persistent volume to **`/data`**. The container runs as UID/GID `10001`; a bind-mounted data directory must be writable by that user.

Put a reverse proxy in front of the app for HTTPS when accessing it over the internet. Cookies become secure when requests use TLS or the proxy supplies `X-Forwarded-Proto: https`.

## First steps

1. Sign in as an administrator and create accounts in **Team & settings → User management**. Give each new user the temporary password `password!!`; the app requires them to choose a new password at first sign-in.
2. Create teams and assign users using **Manage membership**. Users may belong to several teams.
3. Add measurables, rocks, issues, headlines, and to-dos; select a primary owner and any additional assignees.
4. Start an L10, follow the agenda, solve issues, and conclude to save a report.
5. Switch teams with the sidebar selector. Use **⇄** to move individual items, or **Move all items** for a bulk transfer. Conclude any active meeting before moving its report or doing a full transfer.

Removing team membership clears that user's current assignments in that team. Historical reports preserve the original names and assignments. Disabling an account prevents login while preserving records. At least one active administrator must remain.

## Local development

Requires Go 1.24 or newer.

```sh
go run .
go test ./...
go vet ./...
```

Defaults: port `8111`, data directory `./data`. Override using `PORT` and `DATA_DIR`. Frontend assets are embedded into the Go executable, so restart after changing frontend files.

## Operational notes

- Run one app instance per data volume. Persistence and concurrency control are designed for a single process.
- Changes save to the server; notes have explicit Save buttons and also save on agenda transitions. Use Refresh to load changes made in another browser; concurrent edits return a conflict rather than silently overwrite data.
- Sessions are in memory and expire after seven days. Server restarts require a new login. Changing passwords or editing accounts invalidates their existing sessions. Setting a user's temporary password requires them to replace it at their next sign-in.
- Passwords are salted and hashed with PBKDF2-HMAC-SHA256 (600,000 iterations). Login/setup/password attempts are rate-limited.
- Backup restore replaces all teams and accounts, invalidates sessions, and requires login with a restored account. Export a current backup first.
- This is an independent implementation of the EOS Level 10 meeting workflow, not an official EOS product.

Test