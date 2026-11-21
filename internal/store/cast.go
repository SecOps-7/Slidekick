package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Cast devices (Chromecasts) an admin saved, and the devices found on the
// network that an admin hid. The server only connects to saved devices and
// to devices its own discovery found: never to an address an editor typed.

const castSchema = `
CREATE TABLE IF NOT EXISTS cast_devices (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  host       TEXT NOT NULL,
  port       INTEGER NOT NULL DEFAULT 8009,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS cast_hidden (
  key        TEXT PRIMARY KEY,
  name       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
`

// CastDevice is a saved device, or (Auto set, from the service) one
// discovery found and nobody saved.
type CastDevice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
	// DeviceID is the device's own stable id (the mDNS TXT id, or its UDN
	// without dashes), when known: it recognises the device after its
	// address changes.
	DeviceID  string    `json:"deviceId,omitempty"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	// Auto marks a device found on the network but not saved; its ID is
	// "auto-…" and it lives in memory only.
	Auto bool `json:"auto,omitempty"`
	// LastSeen is when discovery last saw the device (nil: not seen).
	LastSeen *time.Time `json:"lastSeen,omitempty"`
}

func (s *Store) ensureCast(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, castSchema); err != nil {
		return err
	}
	// Saved devices gained the device's own id and model.
	for _, col := range []string{"device_id", "model"} {
		var n int
		s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('cast_devices') WHERE name=?`, col).Scan(&n)
		if n == 0 {
			if _, err := s.db.ExecContext(ctx, `ALTER TABLE cast_devices ADD COLUMN `+col+` TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
				return err
			}
		}
	}
	return nil
}

const castCols = `id,name,host,port,created_at,device_id,model`

func scanCast(sc interface{ Scan(...any) error }) (*CastDevice, error) {
	var d CastDevice
	var created string
	if err := sc.Scan(&d.ID, &d.Name, &d.Host, &d.Port, &created, &d.DeviceID, &d.Model); err != nil {
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO cast_devices(`+castCols+`) VALUES(?,?,?,?,?,?,?)`, d.ID, d.Name, d.Host, d.Port, ts, d.DeviceID, d.Model)
	return err
}

// UpdateCastDeviceAddr records where a saved device is now and, when it
// wasn't known yet, its own id and model (learned by discovery).
func (s *Store) UpdateCastDeviceAddr(ctx context.Context, id, host string, port int, deviceID, model string) error {
	if err := s.ensureCast(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE cast_devices SET host=?, port=?,
		device_id=CASE WHEN device_id='' THEN ? ELSE device_id END,
		model=CASE WHEN model='' THEN ? ELSE model END WHERE id=?`, host, port, deviceID, model, id)
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

// HiddenCastDevices maps the keys of devices admins hid from cast menus
// (a device id, or "ip:<host>" for devices without one) to their names.
func (s *Store) HiddenCastDevices(ctx context.Context) (map[string]string, error) {
	if err := s.ensureCast(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT key,name FROM cast_hidden`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, n string
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// SetCastDeviceHidden hides a found device from cast menus, or shows it again.
func (s *Store) SetCastDeviceHidden(ctx context.Context, key, name string, hidden bool) error {
	if err := s.ensureCast(ctx); err != nil {
		return err
	}
	var err error
	if hidden {
		_, err = s.db.ExecContext(ctx, `INSERT INTO cast_hidden(key,name,created_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET name=excluded.name`, key, name, now())
	} else {
		_, err = s.db.ExecContext(ctx, `DELETE FROM cast_hidden WHERE key=?`, key)
	}
	return err
}
