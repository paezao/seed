package memory

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Backup is a copy of my live data.
type Backup struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"` // before_generation, daily, manual, before_restore
	Generation int       `json:"generation"`
	Label      string    `json:"label,omitempty"`
	File       string    `json:"-"`
	Size       int64     `json:"size"`
	CreatedAt  time.Time `json:"created_at"`
}

const backupCols = `id, kind, generation, label, file, size, created_at`

func scanBackup(r pgx.Row) (*Backup, error) {
	var b Backup
	err := r.Scan(&b.ID, &b.Kind, &b.Generation, &b.Label, &b.File, &b.Size, &b.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &b, err
}

func (s *Store) AddBackup(ctx context.Context, b *Backup) error {
	return s.Pool.QueryRow(ctx, `INSERT INTO backups (id, kind, generation, label, file, size) VALUES ($1, $2, $3, $4, $5, $6) RETURNING created_at`,
		b.ID, b.Kind, b.Generation, b.Label, b.File, b.Size).Scan(&b.CreatedAt)
}

func (s *Store) Backup(ctx context.Context, id string) (*Backup, error) {
	return scanBackup(s.Pool.QueryRow(ctx, `SELECT `+backupCols+` FROM backups WHERE id=$1`, id))
}

// Backups lists copies, newest first (only of kind, if given).
func (s *Store) Backups(ctx context.Context, kind string) ([]*Backup, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+backupCols+` FROM backups WHERE $1 = '' OR kind = $1 ORDER BY created_at DESC, id DESC`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LastBackupOf is the newest copy taken while generation n was live (the
// data as it was when n was replaced, if one was taken then).
func (s *Store) LastBackupOf(ctx context.Context, n int) (*Backup, error) {
	return scanBackup(s.Pool.QueryRow(ctx, `SELECT `+backupCols+` FROM backups WHERE generation=$1 AND kind <> 'before_restore'
		ORDER BY created_at DESC, id DESC LIMIT 1`, n))
}

func (s *Store) DeleteBackup(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM backups WHERE id=$1`, id)
	return err
}
