package store

import "encoding/json"

func parseRules(s string) []RoleRule {
	var r []RoleRule
	if json.Unmarshal([]byte(s), &r) != nil || r == nil {
		return []RoleRule{}
	}
	return r
}

func rulesJSON(r []RoleRule) string {
	if r == nil {
		r = []RoleRule{}
	}
	b, _ := json.Marshal(r)
	return string(b)
}
