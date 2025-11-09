package sqlsafe

import (
	"fmt"
	"strings"
)

// DefaultPolicy returns the default read-only policy.
func DefaultPolicy() *Policy {
	p := &Policy{
		allow:    map[string]Decision{},
		block:    map[string]Decision{},
		fallback: Decision{Allow: false, Reason: "keyword %s is not permitted in safe-only mode"},
	}

	for keyword, reason := range map[string]string{
		"SELECT":   "read-only keyword SELECT",
		"SHOW":     "read-only keyword SHOW",
		"DESCRIBE": "read-only keyword DESCRIBE",
	} {
		p.Allow(keyword, reason)
	}

	// Explicit block reasons for destructive or stateful operations.
	for keyword := range map[string]struct{}{
		"ALTER":    {},
		"CALL":     {},
		"CLONE":    {},
		"COMMENT":  {},
		"COMMIT":   {},
		"COPY":     {},
		"CREATE":   {},
		"DELETE":   {},
		"DROP":     {},
		"GRANT":    {},
		"INSERT":   {},
		"MERGE":    {},
		"MSCK":     {},
		"OPTIMIZE": {},
		"REPLACE":  {},
		"RESTORE":  {},
		"REVOKE":   {},
		"ROLLBACK": {},
		"SET":      {},
		"TRUNCATE": {},
		"UPDATE":   {},
		"USE":      {},
		"VACUUM":   {},
	} {
		p.Block(keyword, "found DDL/DML keyword "+keyword)
	}

	return p
}

// Allow registers an allowed keyword with a descriptive reason.
func (p *Policy) Allow(keyword, reason string) {
	keyword = strings.ToUpper(keyword)
	if p.allow == nil {
		p.allow = map[string]Decision{}
	}
	p.allow[keyword] = Decision{Allow: true, Reason: reason}
}

// Block registers a blocked keyword with a descriptive reason.
func (p *Policy) Block(keyword, reason string) {
	keyword = strings.ToUpper(keyword)
	if p.block == nil {
		p.block = map[string]Decision{}
	}
	p.block[keyword] = Decision{Allow: false, Reason: reason}
}

func (p *Policy) decide(keyword string) Decision {
	keyword = strings.ToUpper(keyword)
	if decision, ok := p.block[keyword]; ok {
		return decision
	}
	if decision, ok := p.allow[keyword]; ok {
		return decision
	}
	if p.fallback.Reason != "" {
		return Decision{Allow: false, Reason: fmt.Sprintf(p.fallback.Reason, keyword)}
	}
	return Decision{Allow: false, Reason: "keyword " + keyword + " is not permitted"}
}

func (p *Policy) isKnown(keyword string) bool {
	keyword = strings.ToUpper(keyword)
	if _, ok := p.block[keyword]; ok {
		return true
	}
	if _, ok := p.allow[keyword]; ok {
		return true
	}
	return false
}
