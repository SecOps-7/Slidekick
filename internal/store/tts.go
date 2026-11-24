package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Text-to-speech providers an admin connected. Like AI providers, the API
// key is stored encrypted by the caller (internal/secret) and never leaves
// the server. The default narration provider and voice are instance
// settings (SettingTTSProvider, SettingTTSVoice).

const ttsSchema = `
CREATE TABLE IF NOT EXISTS tts_providers (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  kind          TEXT NOT NULL,
  base_url      TEXT NOT NULL,
  key_enc       TEXT NOT NULL DEFAULT '',
  default_voice TEXT NOT NULL DEFAULT '',
  default_model TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL,
  voices        TEXT NOT NULL DEFAULT ''
);
`

// Settings keys for the default narration voice. An empty provider means
// the browser's own voices.
const (
	SettingTTSProvider = "tts_default_provider"
	SettingTTSVoice    = "tts_default_voice"
)

type TTSProvider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	BaseURL      string `json:"baseUrl"`
	KeyEnc       string `json:"-"`
	HasKey       bool   `json:"hasKey"`
	DefaultVoice string `json:"defaultVoice"`
	DefaultModel string `json:"defaultModel"`
	// Voices are the voices an admin added to the provider, with the names
	// people pick them by. DefaultVoice is one of them (or empty).
	Voices []TTSVoice `json:"voices"`
}

// TTSVoice is one saved voice: the provider's voice id and a name for people.
type TTSVoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Store) ensureTTS(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, ttsSchema); err != nil {
		return err
	}
	// Providers gained a list of named voices.
	var n int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('tts_providers') WHERE name='voices'`).Scan(&n)
	if n == 0 {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE tts_providers ADD COLUMN voices TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	return nil
}

const ttsCols = `id,name,kind,base_url,key_enc,default_voice,default_model,voices`

func scanTTS(sc interface{ Scan(...any) error }) (*TTSProvider, error) {
	var p TTSProvider
	var voices string
	if err := sc.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.KeyEnc, &p.DefaultVoice, &p.DefaultModel, &voices); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.HasKey = p.KeyEnc != ""
	if voices != "" {
		json.Unmarshal([]byte(voices), &p.Voices)
	}
	if len(p.Voices) == 0 && p.DefaultVoice != "" {
		// Saved before voices had names: its one voice, named by its id.
		p.Voices = []TTSVoice{{ID: p.DefaultVoice, Name: p.DefaultVoice}}
	}
	if p.Voices == nil {
		p.Voices = []TTSVoice{}
	}
	return &p, nil
}

func (s *Store) TTSProviders(ctx context.Context) ([]*TTSProvider, error) {
	if err := s.ensureTTS(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+ttsCols+` FROM tts_providers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TTSProvider{}
	for rows.Next() {
		p, err := scanTTS(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetTTSProvider(ctx context.Context, id string) (*TTSProvider, error) {
	if err := s.ensureTTS(ctx); err != nil {
		return nil, err
	}
	return scanTTS(s.db.QueryRowContext(ctx, `SELECT `+ttsCols+` FROM tts_providers WHERE id=?`, id))
}

func (s *Store) SaveTTSProvider(ctx context.Context, p *TTSProvider) error {
	if err := s.ensureTTS(ctx); err != nil {
		return err
	}
	voices, err := json.Marshal(p.Voices)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO tts_providers(`+ttsCols+`,created_at) VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, kind=excluded.kind, base_url=excluded.base_url, key_enc=excluded.key_enc,
 default_voice=excluded.default_voice, default_model=excluded.default_model, voices=excluded.voices`,
		p.ID, p.Name, p.Kind, p.BaseURL, p.KeyEnc, p.DefaultVoice, p.DefaultModel, string(voices), now())
	return err
}

// DeleteTTSProvider removes a provider; when it was the default for
// narration, the default goes back to the browser's voices.
func (s *Store) DeleteTTSProvider(ctx context.Context, id string) error {
	if err := s.ensureTTS(ctx); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM tts_providers WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	if s.Setting(ctx, SettingTTSProvider) == id {
		if err := s.SetSetting(ctx, SettingTTSProvider, ""); err != nil {
			return err
		}
		return s.SetSetting(ctx, SettingTTSVoice, "")
	}
	return nil
}
