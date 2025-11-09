package sqlsafe

import "testing"

func TestParseStatementsSplitsAndTracksPositions(t *testing.T) {
	script := "-- comment\nSELECT 1;\n\nWITH cte AS (SELECT 2)\nSELECT * FROM cte"
	statements := ParseStatements(script)
	if len(statements) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(statements))
	}
	if statements[0].Start.Offset != 0 {
		t.Fatalf("expected first statement offset 0, got %d", statements[0].Start.Offset)
	}
	if statements[1].Start.Offset <= statements[0].Start.Offset {
		t.Fatalf("expected second statement to start after first")
	}

	classifier := NewClassifier(nil)
	results, err := classifier.Classify(statements)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 classification results, got %d", len(results))
	}
	if results[0].Keyword != "SELECT" || results[0].Position.Line != 2 || results[0].Position.Column != 1 {
		t.Fatalf("unexpected first classification: %+v", results[0])
	}
	if results[1].Keyword != "SELECT" || results[1].Position.Line != 5 || results[1].Position.Column != 1 {
		t.Fatalf("unexpected second classification: %+v", results[1])
	}
}

func TestClassifierAllowsReadOnlyStatements(t *testing.T) {
	stmt := "SELECT * FROM table"
	statements := ParseStatements(stmt)
	classifier := NewClassifier(nil)
	results, err := classifier.Classify(statements)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Allowed {
		t.Fatalf("expected statement to be allowed, got %+v", results[0])
	}
	if results[0].Keyword != "SELECT" {
		t.Fatalf("expected keyword SELECT, got %s", results[0].Keyword)
	}
}

func TestClassifierBlocksDestructiveStatements(t *testing.T) {
	stmt := "CREATE TABLE t(id INT)"
	statements := ParseStatements(stmt)
	classifier := NewClassifier(nil)
	results, err := classifier.Classify(statements)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Allowed {
		t.Fatalf("expected statement to be blocked, got %+v", results[0])
	}
	if results[0].Keyword != "CREATE" {
		t.Fatalf("expected keyword CREATE, got %s", results[0].Keyword)
	}
}

func TestClassifierHandlesWithClause(t *testing.T) {
	stmt := `WITH cte AS (SELECT 1) SELECT * FROM cte`
	statements := ParseStatements(stmt)
	classifier := NewClassifier(nil)
	results, err := classifier.Classify(statements)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Allowed || results[0].Keyword != "SELECT" {
		t.Fatalf("WITH SELECT should be allowed, got %+v", results[0])
	}
}

func TestClassifierBlocksWithClauseLeadingToWrite(t *testing.T) {
	stmt := `WITH cte AS (SELECT 1) INSERT INTO t SELECT * FROM cte`
	statements := ParseStatements(stmt)
	classifier := NewClassifier(nil)
	results, err := classifier.Classify(statements)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Allowed || results[0].Keyword != "INSERT" {
		t.Fatalf("expected INSERT to be blocked, got %+v", results[0])
	}
}

func TestClassifierExplainFollowsTarget(t *testing.T) {
	stmt := "EXPLAIN SELECT * FROM t"
	statements := ParseStatements(stmt)
	classifier := NewClassifier(nil)
	results, err := classifier.Classify(statements)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Allowed || results[0].Keyword != "SELECT" {
		t.Fatalf("expected EXPLAIN SELECT to be allowed, got %+v", results[0])
	}

	stmt = "EXPLAIN CREATE TABLE t(id INT)"
	statements = ParseStatements(stmt)
	results, err = classifier.Classify(statements)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Allowed || results[0].Keyword != "CREATE" {
		t.Fatalf("expected EXPLAIN CREATE to be blocked, got %+v", results[0])
	}
}

func TestClassifierIgnoresComments(t *testing.T) {
	stmt := "/* drop */ SELECT 1 -- insert\n"
	statements := ParseStatements(stmt)
	classifier := NewClassifier(nil)
	results, err := classifier.Classify(statements)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Allowed || results[0].Keyword != "SELECT" {
		t.Fatalf("comments should be ignored, got %+v", results[0])
	}
}
