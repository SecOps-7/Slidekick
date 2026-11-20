package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// AI providers an admin connected. The API key is stored encrypted by the
// caller (internal/secret) and never leaves the server.

const aiSchema = `
CREATE TABLE IF NOT EXISTS ai_providers (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  kind       TEXT NOT NULL,
  base_url   TEXT NOT NULL,
  key_enc    TEXT NOT NULL DEFAULT '',
  models     TEXT NOT NULL DEFAULT '[]',
  enabled    INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL
);
`

type AIProvider struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	BaseURL string   `json:"baseUrl"`
	KeyEnc  string   `json:"-"`
	HasKey  bool     `json:"hasKey"`
	Models  []string `json:"models"`
	Enabled bool     `json:"enabled"`
}

func (s *Store) ensureAI(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, aiSchema)
	return err
}

const aiCols = `id,name,kind,base_url,key_enc,models,enabled`

func scanAI(sc interface{ Scan(...any) error }) (*AIProvider, error) {
	var p AIProvider
	var models string
	var en int
	if err := sc.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.KeyEnc, &models, &en); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if json.Unmarshal([]byte(models), &p.Models) != nil || p.Models == nil {
		p.Models = []string{}
	}
	p.Enabled, p.HasKey = en == 1, p.KeyEnc != ""
	return &p, nil
}

func (s *Store) AIProviders(ctx context.Context) ([]*AIProvider, error) {
	if err := s.ensureAI(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+aiCols+` FROM ai_providers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AIProvider{}
	for rows.Next() {
		p, err := scanAI(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetAIProvider(ctx context.Context, id string) (*AIProvider, error) {
	if err := s.ensureAI(ctx); err != nil {
		return nil, err
	}
	return scanAI(s.db.QueryRowContext(ctx, `SELECT `+aiCols+` FROM ai_providers WHERE id=?`, id))
}

func (s *Store) SaveAIProvider(ctx context.Context, p *AIProvider) error {
	if err := s.ensureAI(ctx); err != nil {
		return err
	}
	if p.Models == nil {
		p.Models = []string{}
	}
	models, _ := json.Marshal(p.Models)
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_providers(`+aiCols+`,created_at) VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, kind=excluded.kind, base_url=excluded.base_url, key_enc=excluded.key_enc,
 models=excluded.models, enabled=excluded.enabled`,
		p.ID, p.Name, p.Kind, p.BaseURL, p.KeyEnc, string(models), b2i(p.Enabled), now())
	return err
}

func (s *Store) DeleteAIProvider(ctx context.Context, id string) error {
	if err := s.ensureAI(ctx); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM ai_providers WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}
