package sqlsafe

import (
	"fmt"
	"strings"
)

// NewClassifier constructs a Classifier using the provided policy. If policy is nil the default policy is used.
func NewClassifier(policy *Policy) *Classifier {
	if policy == nil {
		policy = DefaultPolicy()
	}
	known := map[string]struct{}{}
	for k := range policy.allow {
		known[k] = struct{}{}
	}
	for k := range policy.block {
		known[k] = struct{}{}
	}
	// Control keywords that participate in classification but are not part of allow/block maps.
	for _, kw := range []string{"WITH", "RECURSIVE", "AS", "EXPLAIN", "PLAN", "FORMATTED", "EXTENDED", "LOGICAL", "PHYSICAL", "ANALYZE"} {
		known[kw] = struct{}{}
	}
	return &Classifier{policy: policy, known: known}
}

// Classify analyses each statement and returns the classification results.
func (c *Classifier) Classify(statements []Statement) ([]Classification, error) {
	results := make([]Classification, 0, len(statements))
	for _, stmt := range statements {
		res, err := c.classifyStatement(stmt)
		if err != nil {
			return nil, err
		}
		results = append(results, res)
	}
	return results, nil
}

func (c *Classifier) classifyStatement(stmt Statement) (Classification, error) {
	r := newReader(stmt)
	chain := []string{}
	for {
		token, pos, depth, ok := r.nextToken()
		if !ok {
			return Classification{
				Statement: stmt,
				Keyword:   "",
				Position:  pos,
				Allowed:   true,
				Reason:    "empty statement",
				Chain:     chain,
			}, nil
		}
		if depth > 0 {
			continue
		}
		keyword := strings.ToUpper(token)
		if keyword == "" {
			continue
		}
		switch keyword {
		case "WITH":
			chain = append(chain, keyword)
			return c.classifyWith(stmt, r, chain)
		case "EXPLAIN":
			chain = append(chain, keyword)
			return c.classifyExplain(stmt, r, chain)
		default:
			if !c.policy.isKnown(keyword) {
				// Likely an identifier (e.g., CTE name); skip until we find a recognised keyword.
				continue
			}
			decision := c.policy.decide(keyword)
			return Classification{
				Statement: stmt,
				Keyword:   keyword,
				Position:  pos,
				Allowed:   decision.Allow,
				Reason:    decision.Reason,
				Chain:     append(chain, keyword),
			}, nil
		}
	}
}

func (c *Classifier) classifyWith(stmt Statement, r *reader, chain []string) (Classification, error) {
	for {
		token, pos, depth, ok := r.nextToken()
		if !ok {
			return Classification{
				Statement: stmt,
				Keyword:   "WITH",
				Position:  pos,
				Allowed:   false,
				Reason:    "WITH clause without subsequent statement",
				Chain:     chain,
			}, nil
		}
		if depth > 0 {
			continue
		}
		keyword := strings.ToUpper(token)
		switch keyword {
		case "WITH", "RECURSIVE", "AS":
			chain = append(chain, keyword)
			continue
		}
		if !c.policy.isKnown(keyword) {
			// Ignore identifiers (CTE names, aliases) at depth 0.
			continue
		}
		decision := c.policy.decide(keyword)
		return Classification{
			Statement: stmt,
			Keyword:   keyword,
			Position:  pos,
			Allowed:   decision.Allow,
			Reason:    decision.Reason,
			Chain:     append(chain, keyword),
		}, nil
	}
}

func (c *Classifier) classifyExplain(stmt Statement, r *reader, chain []string) (Classification, error) {
	modifiers := map[string]struct{}{
		"PLAN":      {},
		"FORMATTED": {},
		"EXTENDED":  {},
		"LOGICAL":   {},
		"PHYSICAL":  {},
		"ANALYZE":   {},
	}
	for {
		token, pos, depth, ok := r.nextToken()
		if !ok {
			return Classification{
				Statement: stmt,
				Keyword:   "EXPLAIN",
				Position:  pos,
				Allowed:   false,
				Reason:    "EXPLAIN without target statement",
				Chain:     chain,
			}, nil
		}
		if depth > 0 {
			continue
		}
		keyword := strings.ToUpper(token)
		if _, skip := modifiers[keyword]; skip {
			chain = append(chain, keyword)
			continue
		}
		if !c.policy.isKnown(keyword) {
			return Classification{
				Statement: stmt,
				Keyword:   keyword,
				Position:  pos,
				Allowed:   false,
				Reason:    fmt.Sprintf("EXPLAIN target keyword %s is not permitted", keyword),
				Chain:     append(chain, keyword),
			}, nil
		}
		decision := c.policy.decide(keyword)
		reason := decision.Reason
		if decision.Allow {
			reason = fmt.Sprintf("EXPLAIN of %s is permitted", keyword)
		} else {
			reason = fmt.Sprintf("EXPLAIN target blocked: %s", decision.Reason)
		}
		return Classification{
			Statement: stmt,
			Keyword:   keyword,
			Position:  pos,
			Allowed:   decision.Allow,
			Reason:    reason,
			Chain:     append(chain, keyword),
		}, nil
	}
}
