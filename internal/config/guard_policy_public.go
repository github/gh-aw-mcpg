package config

import "strings"

// ForcePublicRepos overrides the allow-only scope, preserving all other fields.
// It returns false for nil or write-sink-only policies, which enforce public
// output restrictions through sink visibility instead.
func (p *GuardPolicy) ForcePublicRepos() bool {
	if p == nil || (p.AllowOnly == nil && p.WriteSink != nil) {
		return false
	}
	if p.AllowOnly == nil {
		p.AllowOnly = &AllowOnlyPolicy{MinIntegrity: IntegrityNone}
	}
	p.AllowOnly.Repos = "public"
	return true
}

// ForcePublicReposInMap applies ForcePublicRepos to a raw policy map without
// discarding its other fields or changing canonical/legacy policy key spelling.
// A write-sink key, even with a null value, prevents injecting allow-only.
func ForcePublicReposInMap(raw map[string]interface{}) bool {
	if raw == nil {
		return false
	}

	allowOnlyKeys := make([]string, 0, 1)
	var existingAllowOnly map[string]interface{}
	policy := &GuardPolicy{}
	for key, value := range raw {
		switch strings.ToLower(key) {
		case "allow-only", "allowonly":
			allowOnlyKeys = append(allowOnlyKeys, key)
			if allowOnly, ok := value.(map[string]interface{}); ok && allowOnly != nil {
				policy.AllowOnly = &AllowOnlyPolicy{}
				if existingAllowOnly == nil {
					existingAllowOnly = allowOnly
				}
			}
		case "write-sink", "writesink":
			policy.WriteSink = &WriteSinkPolicy{}
		}
	}
	if !policy.ForcePublicRepos() {
		return false
	}
	if len(allowOnlyKeys) == 0 {
		allowOnlyKeys = append(allowOnlyKeys, "allow-only")
	}
	for _, key := range allowOnlyKeys {
		if allowOnly, ok := raw[key].(map[string]interface{}); ok && allowOnly != nil {
			setPublicRepos(allowOnly)
		}
	}
	for _, key := range allowOnlyKeys {
		if allowOnly, ok := raw[key].(map[string]interface{}); !ok || allowOnly == nil {
			replacement := make(map[string]interface{})
			if existingAllowOnly != nil {
				for field, value := range existingAllowOnly {
					replacement[field] = value
				}
			} else {
				replacement["min-integrity"] = IntegrityNone
			}
			setPublicRepos(replacement)
			raw[key] = replacement
		}
	}
	return true
}

func setPublicRepos(allowOnly map[string]interface{}) {
	foundRepos := false
	for key := range allowOnly {
		if strings.EqualFold(key, "repos") {
			allowOnly[key] = "public"
			foundRepos = true
		}
	}
	if !foundRepos {
		allowOnly["repos"] = "public"
	}
}
