#!/usr/bin/env bash
#
# Pack the Windows single-machine, no-Docker Multica distribution.
#
# Produces a self-contained directory (double-click to run) for a Windows
# machine with no Docker / VM / WSL and no internet access:
#
#   dist/multica-win/
#   ├── StartMultica.exe     # double-click entry
#   ├── StopMultica.exe
#   ├── data/                # created at first run
#   ├── postgres/            # portable PostgreSQL 17 (from EDGE/zip)
#   ├── server.exe           # Go backend
#   ├── migrate.exe
#   ├── multica.exe          # CLI + agent daemon
#   └── client/Multica.exe   # Electron desktop client
#
# Run on a machine with internet + Go. Requires a Windows machine to build
# the Electron client (electron-builder Windows target). See
# docs/windows-single-machine-no-docker-plan.md.
#
# Usage:
#   bash scripts/pack-windows.sh [--no-client] [--pg-zip path/to/postgresql.zip]
#   bash scripts/pack-windows.sh --client-dir path/to/win-unpacked [--pg-zip path] [--pg-dir path]
#
# --client-dir points at an already-built electron-builder `win-unpacked`
# directory (e.g. apps/desktop/dist/win-unpacked) whose contents are copied
# into client/. This is the portable form the launcher expects and lets the
# pack run on a non-Windows host.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUT="$REPO_ROOT/dist/multica-win"

NO_CLIENT=0
PG_ZIP=""
PG_DIR=""
CLIENT_DIR=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --no-client) NO_CLIENT=1 ;;
    --pg-zip=*) PG_ZIP="${1#*=}" ;;
    --pg-dir=*) PG_DIR="${1#*=}" ;;
    --client-dir=*) CLIENT_DIR="${1#*=}" ;;
    --client-dir) shift; CLIENT_DIR="$1" ;;
    *) echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
  shift
done

echo "==> Packing Multica Windows distribution into $OUT"

mkdir -p "$OUT/postgres" "$OUT/client" "$OUT/data"

# ---------------------------------------------------------------------------
# 1. Backend binaries (cross-compiled from any host)
# ---------------------------------------------------------------------------
echo "==> Cross-compiling server.exe / migrate.exe / multica.exe"
(
  cd "$REPO_ROOT/server"
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o "$OUT/server.exe"   ./cmd/server
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o "$OUT/migrate.exe"  ./cmd/migrate
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o "$OUT/multica.exe"  ./cmd/multica
)

# ---------------------------------------------------------------------------
# 2. Launcher (StartMultica.exe / StopMultica.exe)
# ---------------------------------------------------------------------------
echo "==> Cross-compiling launcher"
(
  cd "$REPO_ROOT/tools/windows-launcher"
  GOOS=windows GOARCH=amd64 go build -o "$OUT/StartMultica.exe" .
  GOOS=windows GOARCH=amd64 go build -o "$OUT/StopMultica.exe"  .
)

# ---------------------------------------------------------------------------
# 3. Portable PostgreSQL 17
# ---------------------------------------------------------------------------
if [ -n "$PG_DIR" ]; then
  echo "==> Copying portable PostgreSQL from $PG_DIR"
  if [ ! -d "$PG_DIR" ]; then
    echo "Error: --pg-dir does not exist or is not a directory: $PG_DIR" >&2
    exit 1
  fi
  rm -rf "$OUT/postgres"
  mkdir -p "$OUT/postgres"
  # The zip contains a top-level pgsql/ dir; accept both the dir and the zip.
  SRC="$PG_DIR"
  if [ -d "$PG_DIR/pgsql" ]; then
    SRC="$PG_DIR/pgsql"
  fi
  if [ ! -d "$SRC/bin" ]; then
    echo "Error: no bin/ under $PG_DIR (expected pgsql/bin or bin)." >&2
    exit 1
  fi
  shopt -s dotglob
  cp -R "$SRC"/* "$OUT/postgres/"
  shopt -u dotglob
elif [ -n "$PG_ZIP" ]; then
  echo "==> Extracting portable PostgreSQL from $PG_ZIP"
  rm -rf "$OUT/postgres"
  mkdir -p "$OUT/postgres"
  unzip -q "$PG_ZIP" -d "$OUT/postgres"
  # The zip contains a top-level pgsql/ dir; flatten it.
  if [ -d "$OUT/postgres/pgsql" ]; then
    shopt -s dotglob
    mv "$OUT/postgres/pgsql"/* "$OUT/postgres/"
    rm -rf "$OUT/postgres/pgsql"
    shopt -u dotglob
  fi
else
  echo "==> Portable PostgreSQL not provided (--pg-zip=... or --pg-dir=...)."
  echo "    Place the official postgresql-<ver>-windows-x64-binaries.zip at:"
  echo "      $OUT/postgres/  (extracted)"
  echo "    Skipping for now."
fi

# ---------------------------------------------------------------------------
# 4. Electron client
# ---------------------------------------------------------------------------
if [ -n "$CLIENT_DIR" ]; then
  echo "==> Copying Electron client from --client-dir=$CLIENT_DIR"
  if [ ! -d "$CLIENT_DIR" ]; then
    echo "Error: --client-dir does not exist or is not a directory: $CLIENT_DIR" >&2
    exit 1
  fi
  if [ ! -f "$CLIENT_DIR/Multica.exe" ]; then
    echo "Error: $CLIENT_DIR has no Multica.exe (not an electron-builder win-unpacked dir?)" >&2
    exit 1
  fi
  rm -rf "$OUT/client"
  mkdir -p "$OUT/client"
  cp -R "$CLIENT_DIR"/. "$OUT/client/"
  echo "==> Client copied (Multica.exe present: $([ -f "$OUT/client/Multica.exe" ] && echo yes || echo no))"
elif [ "$NO_CLIENT" -eq 1 ]; then
  echo "==> Skipping Electron client (--no-client)"
elif [ "$(uname -s)" = "MINGW"* ] || [ "$(uname -s)" = "MSYS"* ] || [ "$(uname -s)" = "CYGWIN"* ]; then
  echo "==> Building Electron client on Windows host"
  (
    cd "$REPO_ROOT/apps/desktop"
    pnpm install
    pnpm build
    pnpm package -- --win
  )
  # electron-builder outputs into apps/desktop/dist; copy the installer or
  # portable exe into client/.
  find "$REPO_ROOT/apps/desktop/dist" -maxdepth 1 \( -iname "*.exe" -o -iname "*.msi" \) -exec cp {} "$OUT/client/" \;
else
  echo "==> Electron client requires a Windows host to build (electron-builder)."
  echo "    Build it separately and pass --client-dir path/to/win-unpacked, or"
  echo "    copy the .exe into: $OUT/client/"
fi

# ---------------------------------------------------------------------------
# 5. Database migrations (migrate.exe resolves these relative to its own
#    directory, so they must be present in the distribution).
# ---------------------------------------------------------------------------
echo "==> Copying database migrations"
if [ -d "$REPO_ROOT/server/migrations" ]; then
  mkdir -p "$OUT/migrations"
  cp -R "$REPO_ROOT/server/migrations"/. "$OUT/migrations/"
  echo "==> Migrations copied ($(ls "$OUT/migrations"/*.sql 2>/dev/null | wc -l | tr -d ' ') sql files)"
else
  echo "Error: server/migrations not found at $REPO_ROOT/server/migrations" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# 6. README
# ---------------------------------------------------------------------------
cat > "$OUT/README.txt" <<'EOF'
Multica — Windows single-machine, no-Docker distribution

RUN
  Double-click StartMultica.exe. It will:
    - initialize a local PostgreSQL data dir (first run)
    - run migrations
    - start the backend on http://127.0.0.1:8080
    - open the Multica desktop client

LOGIN
  Verification code is 888888 (local development mode).
  Or read it from data/launcher.log.

STOP
  Double-click StopMultica.exe, or close the client window.

DATA
  Everything lives in data/ (pgdata, uploads, jwt_secret).
  Back it up by copying the data/ directory.

TROUBLESHOOT
  Logs: data/launcher.log and data/postgres.log
EOF

echo
echo "==> Done. Distribution at: $OUT"
du -sh "$OUT"
