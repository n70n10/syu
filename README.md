# syu

**Tracked `pacman -Syu` with rollback** — a minimal Go tool for EndeavourOS/Arch that records every system upgrade in a local SQLite database and lets you undo it cleanly.

---

## Features

| Command | What it does |
|---|---|
| `sudo syu` | Run `pacman -Syu`, snapshot before/after, record changes |
| `sudo syu rollback` | Undo the last upgrade (or any session by ID) |
| `sudo syu rollback --dry-run` | Preview what rollback would do |
| `syu list` | Show all recorded upgrade sessions |
| `syu info [ID]` | Full package diff for a session |
| `syu delta [ID1 ID2]` | Compare package versions between two sessions |
| `syu history <pkg>` | Full version history for a specific package |
| `syu stats` | Database statistics |
| `syu cache` | Pacman cache size / file count |
| `sudo syu prune [N]` | Delete old sessions, keep the most recent N |

---

## Install

### Requirements

- Go ≥ 1.22
- `gcc` (required for CGO / sqlite3)
- `vercmp` (ships with pacman, already on your system)

```bash
# Install build deps if needed
sudo pacman -S go gcc

# Clone and build
git clone https://github.com/n70n10/syu
cd syu
make install   # builds + copies to /usr/local/bin/syu
```

---

## Usage

### Upgrade

```bash
sudo syu
# or equivalently:
sudo syu upgrade
```

Live `pacman -Syu` output is streamed to your terminal as normal. After pacman exits, syu diffs the package state and records a session to `/var/lib/syu/syu.db`.

### Rollback

```bash
# Undo the most recent upgrade session
sudo syu rollback

# Preview what would happen (no changes made)
sudo syu rollback --dry-run

# Roll back a specific session
sudo syu rollback 4
```

**How rollback works:**

| What happened during upgrade | Rollback action |
|---|---|
| Package upgraded A→B | Downgraded back to A |
| Package newly installed | Removed |
| Package removed | Reinstalled at its old version |

**Resolution priority:** local pacman cache (`/var/cache/pacman/pkg`) → official repos → **abort gracefully**.

If *any* package is unresolvable (not in cache, not in repos at the required version), the rollback **aborts before touching anything**. No partial states.

> **Tip:** Keep a few versions of cached packages around with `paccache -rk3` instead of `paccache -rk1`. This gives rollback more options.

### Inspect history

```bash
# List recent sessions
syu list
syu list 50    # show up to 50

# Full diff for session #3
syu info 3

# Compare two sessions (what changed between upgrades)
syu delta 3 7

# Where has 'linux' been through the upgrade history?
syu history linux

# Database totals
syu stats

# Is there enough cache for rollbacks?
syu cache
```

---

## Database

SQLite at `/var/lib/syu/syu.db`. Schema:

```sql
sessions        (id, timestamp, label, status)
package_changes (id, session_id, name, change_type, old_version, new_version)
```

`status` is either `completed` or `rolled_back`.

---

## Project structure

```
syu/
├── cmd/syu/main.go          CLI entry point, all command handlers
├── internal/
│   ├── db/db.go               SQLite layer
│   ├── pacman/pacman.go       Snapshot, diff, cache resolution, install/remove
│   ├── rollback/rollback.go   Plan building, preview, execution
│   └── ui/ui.go               Tables, colors, formatting
├── Makefile
└── go.mod
```

---

## Caveats

- Rollback depends on packages being available in `/var/cache/pacman/pkg` or from repos. Aggressive cache cleaning (`paccache -rk1` or `pacman -Scc`) will reduce rollback coverage.
- AUR packages are **not** automatically handled. If you upgraded an AUR package, you'll need to rebuild/downgrade it manually using your AUR helper.
- Rolling back kernel upgrades while the new kernel is running will take effect only after reboot.

---

## License

MIT
