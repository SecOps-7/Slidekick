package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Cast devices (Chromecasts) an admin added. Editors can only cast to these:
// the server never connects to an address someone else typed.

const castSchema = `
CREATE TABLE IF NOT EXISTS cast_devices (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  host       TEXT NOT NULL,
  port       INTEGER NOT NULL DEFAULT 8009,
  created_at TEXT NOT NULL
);
`

type CastDevice struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) ensureCast(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, castSchema)
	return err
}

const castCols = `id,name,host,port,created_at`

func scanCast(sc interface{ Scan(...any) error }) (*CastDevice, error) {
	var d CastDevice
	var created string
	if err := sc.Scan(&d.ID, &d.Name, &d.Host, &d.Port, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &d, nil
}

func (s *Store) CastDevices(ctx context.Context) ([]*CastDevice, error) {
	if err := s.ensureCast(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+castCols+` FROM cast_devices ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*CastDevice{}
	for rows.Next() {
		d, err := scanCast(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetCastDevice(ctx context.Context, id string) (*CastDevice, error) {
	if err := s.ensureCast(ctx); err != nil {
		return nil, err
	}
	return scanCast(s.db.QueryRowContext(ctx, `SELECT `+castCols+` FROM cast_devices WHERE id=?`, id))
}

// AddCastDevice stores a new device; CreatedAt is set here.
func (s *Store) AddCastDevice(ctx context.Context, d *CastDevice) error {
	if err := s.ensureCast(ctx); err != nil {
		return err
	}
	if d.Port <= 0 {
		d.Port = 8009
	}
	ts := now()
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
	_, err := s.db.ExecContext(ctx, `INSERT INTO cast_devices(`+castCols+`) VALUES(?,?,?,?,?)`, d.ID, d.Name, d.Host, d.Port, ts)
	return err
}

func (s *Store) DeleteCastDevice(ctx context.Context, id string) error {
	if err := s.ensureCast(ctx); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM cast_devices WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}
