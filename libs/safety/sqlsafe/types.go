package sqlsafe

// Position identifies a location within a SQL script.
type Position struct {
	// Line is the 1-indexed line number.
	Line int
	// Column is the 1-indexed column number.
	Column int
	// Offset is the 0-indexed byte offset relative to the start of the script.
	Offset int
}

// Statement represents a single SQL statement extracted from a script.
type Statement struct {
	// Text holds the raw statement, including any leading or trailing whitespace.
	Text string
	// Start identifies the position of the first byte in Text relative to the source script.
	Start Position
}

// Decision describes the outcome of evaluating a keyword against a policy.
type Decision struct {
	Allow  bool
	Reason string
}

// Policy encapsulates the allow/block configuration for SQL keywords.
type Policy struct {
	allow    map[string]Decision
	block    map[string]Decision
	fallback Decision
}

// Classification summarises how a statement was evaluated.
type Classification struct {
	Statement Statement
	Keyword   string
	Position  Position
	Allowed   bool
	Reason    string
	Chain     []string
}

// Classifier evaluates SQL statements using a Policy.
type Classifier struct {
	policy *Policy
	known  map[string]struct{}
}
