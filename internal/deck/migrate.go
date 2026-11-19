package deck

import (
	"encoding/json"
	"fmt"
)

// migrations[n] upgrades a decoded deck from schema version n to n+1.
// Version 1 is the first; add entries here when the schema changes.
var migrations = map[int]func(map[string]any) error{}

// Migrate upgrades a raw deck to CurrentSchemaVersion. It returns the input
// unchanged when no migration is needed.
func Migrate(raw []byte) ([]byte, bool, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, false, err
	}
	ver, _ := doc["schemaVersion"].(float64)
	v := int(ver)
	if v == CurrentSchemaVersion {
		return raw, false, nil
	}
	if v < 1 || v > CurrentSchemaVersion {
		return nil, false, fmt.Errorf("unsupported deck schemaVersion %v", doc["schemaVersion"])
	}
	for ; v < CurrentSchemaVersion; v++ {
		m, ok := migrations[v]
		if !ok {
			return nil, false, fmt.Errorf("no migration from schema version %d", v)
		}
		if err := m(doc); err != nil {
			return nil, false, fmt.Errorf("migrating from v%d: %w", v, err)
		}
		doc["schemaVersion"] = v + 1
	}
	out, err := json.Marshal(doc)
	return out, true, err
}
