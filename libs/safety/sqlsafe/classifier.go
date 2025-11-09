package sqlsafe

import (
	"fmt"
	"strings"
	"unicode"
)

// Classifier evaluates statements against a safety policy.
type Classifier struct {
	Policy Policy
}

// NewClassifier constructs a Classifier using the provided policy.
func NewClassifier(policy Policy) Classifier {
	return Classifier{Policy: policy}
}

// Check verifies that every statement complies with the classifier policy.
func (c Classifier) Check(statements []Statement) error {
	for i, stmt := range statements {
		tokens := tokenize(stmt.Text)
		if len(tokens) == 0 {
			continue
		}
		decision := c.Policy.decide(stmt, tokens)
		if decision.Allow {
			continue
		}
		keyword := decision.BlockedKeyword
		if keyword == "" {
			keyword = tokens[0]
		}
		pos := stmt.FirstKeywordPos
		if pos.Line == 0 {
			pos.Line = 1
		}
		if pos.Column == 0 {
			pos.Column = 1
		}
		reason := decision.Reason
		if reason == "" {
			reason = fmt.Sprintf("blocked keyword %s", keyword)
		} else {
			reason = fmt.Sprintf("%s", reason)
		}
		reason = fmt.Sprintf("%s at statement %d (line %d, column %d)", reason, i+1, pos.Line, pos.Column)
		return &Violation{
			StatementIndex: i,
			Keyword:        keyword,
			Position:       pos,
			Reason:         reason,
		}
	}
	return nil
}

func tokenize(sql string) []string {
	tokens := []string{}
	var current strings.Builder
	for _, r := range sql {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$':
			current.WriteRune(unicode.ToUpper(r))
		default:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			switch r {
			case '(', ')', ',':
				tokens = append(tokens, string(r))
			}
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}
