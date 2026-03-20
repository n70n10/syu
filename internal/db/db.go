package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	timestamp TEXT    NOT NULL,
	label     TEXT,
	status    TEXT    NOT NULL DEFAULT 'completed' -- completed | rolled_back
);

CREATE TABLE IF NOT EXISTS package_changes (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id  INTEGER NOT NULL REFERENCES sessions(id),
	name        TEXT    NOT NULL,
	change_type TEXT    NOT NULL, -- upgraded | installed | removed | downgraded
	old_version TEXT,
	new_version TEXT
);

CREATE INDEX IF NOT EXISTS idx_changes_session ON package_changes(session_id);
CREATE INDEX IF NOT EXISTS idx_changes_name    ON package_changes(name);
`

const DefaultDBPath = "/var/lib/syu/syu.db"

type DB struct {
	conn *sql.DB
}

type Session struct {
	ID        int64
	Timestamp time.Time
	Label     string
	Status    string
	Changes   []PackageChange
}

type PackageChange struct {
	ID         int64
	SessionID  int64
	Name       string
	ChangeType string // upgraded | installed | removed | downgraded
	OldVersion string
	NewVersion string
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	conn, err := sql.Open("sqlite3", path+"?_journal=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &DB{conn: conn}, nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

// CreateSession inserts a new upgrade session and returns its ID.
func (d *DB) CreateSession(label string) (int64, error) {
	res, err := d.conn.Exec(
		`INSERT INTO sessions (timestamp, label, status) VALUES (?, ?, 'completed')`,
		time.Now().UTC().Format(time.RFC3339), label,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RecordChanges bulk-inserts package changes for a session.
func (d *DB) RecordChanges(sessionID int64, changes []PackageChange) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO package_changes (session_id, name, change_type, old_version, new_version)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, c := range changes {
		if _, err := stmt.Exec(sessionID, c.Name, c.ChangeType, c.OldVersion, c.NewVersion); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// MarkRolledBack marks a session as rolled_back.
func (d *DB) MarkRolledBack(sessionID int64) error {
	_, err := d.conn.Exec(`UPDATE sessions SET status = 'rolled_back' WHERE id = ?`, sessionID)
	return err
}

// ListSessions returns all sessions, newest first, with change counts.
func (d *DB) ListSessions(limit int) ([]SessionSummary, error) {
	query := `
		SELECT s.id, s.timestamp, COALESCE(s.label,''), s.status,
		       COUNT(c.id) as total,
		       SUM(CASE WHEN c.change_type='upgraded'   THEN 1 ELSE 0 END),
		       SUM(CASE WHEN c.change_type='installed'  THEN 1 ELSE 0 END),
		       SUM(CASE WHEN c.change_type='removed'    THEN 1 ELSE 0 END),
		       SUM(CASE WHEN c.change_type='downgraded' THEN 1 ELSE 0 END)
		FROM sessions s
		LEFT JOIN package_changes c ON c.session_id = s.id
		GROUP BY s.id
		ORDER BY s.id DESC`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := d.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SessionSummary
	for rows.Next() {
		var ss SessionSummary
		var tsStr string
		if err := rows.Scan(&ss.ID, &tsStr, &ss.Label, &ss.Status,
			&ss.Total, &ss.Upgraded, &ss.Installed, &ss.Removed, &ss.Downgraded); err != nil {
			return nil, err
		}
		ss.Timestamp, _ = time.Parse(time.RFC3339, tsStr)
		out = append(out, ss)
	}
	return out, rows.Err()
}

type SessionSummary struct {
	ID        int64
	Timestamp time.Time
	Label     string
	Status    string
	Total     int
	Upgraded  int
	Installed int
	Removed   int
	Downgraded int
}

// GetSession returns full session details including all package changes.
func (d *DB) GetSession(id int64) (*Session, error) {
	s := &Session{}
	var tsStr string
	err := d.conn.QueryRow(
		`SELECT id, timestamp, COALESCE(label,''), status FROM sessions WHERE id = ?`, id,
	).Scan(&s.ID, &tsStr, &s.Label, &s.Status)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session %d not found", id)
	}
	if err != nil {
		return nil, err
	}
	s.Timestamp, _ = time.Parse(time.RFC3339, tsStr)

	rows, err := d.conn.Query(
		`SELECT id, session_id, name, change_type, COALESCE(old_version,''), COALESCE(new_version,'')
		 FROM package_changes WHERE session_id = ? ORDER BY name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c PackageChange
		if err := rows.Scan(&c.ID, &c.SessionID, &c.Name, &c.ChangeType, &c.OldVersion, &c.NewVersion); err != nil {
			return nil, err
		}
		s.Changes = append(s.Changes, c)
	}
	return s, rows.Err()
}

// LatestSession returns the most recent completed session.
func (d *DB) LatestSession() (*Session, error) {
	var id int64
	err := d.conn.QueryRow(
		`SELECT id FROM sessions WHERE status = 'completed' ORDER BY id DESC LIMIT 1`,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no completed sessions found")
	}
	if err != nil {
		return nil, err
	}
	return d.GetSession(id)
}

// PackageHistory returns all recorded changes for a given package name.
func (d *DB) PackageHistory(name string) ([]PackageChange, error) {
	rows, err := d.conn.Query(`
		SELECT c.id, c.session_id, c.name, c.change_type,
		       COALESCE(c.old_version,''), COALESCE(c.new_version,'')
		FROM package_changes c
		JOIN sessions s ON s.id = c.session_id
		WHERE c.name = ?
		ORDER BY s.id DESC`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PackageChange
	for rows.Next() {
		var c PackageChange
		if err := rows.Scan(&c.ID, &c.SessionID, &c.Name, &c.ChangeType, &c.OldVersion, &c.NewVersion); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteSession removes a session and its changes (for prune command).
func (d *DB) DeleteSession(id int64) error {
	_, err := d.conn.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// Stats returns aggregate statistics.
func (d *DB) Stats() (Stats, error) {
	var s Stats
	err := d.conn.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&s.TotalSessions)
	if err != nil {
		return s, err
	}
	err = d.conn.QueryRow(`SELECT COUNT(*) FROM package_changes`).Scan(&s.TotalChanges)
	if err != nil {
		return s, err
	}
	err = d.conn.QueryRow(`SELECT COUNT(DISTINCT name) FROM package_changes`).Scan(&s.UniquePackages)
	return s, err
}

type Stats struct {
	TotalSessions  int
	TotalChanges   int
	UniquePackages int
}
