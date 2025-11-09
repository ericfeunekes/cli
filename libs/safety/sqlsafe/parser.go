package sqlsafe

import "strings"

// ParseStatements splits a SQL script into individual statements while tracking positions.
func ParseStatements(script string) []Statement {
	var statements []Statement

	line, column := 1, 1
	offset := 0

	startIndex := 0
	startLine, startColumn, startOffset := line, column, offset

	inSingle := false
	inDouble := false
	inBacktick := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(script); {
		ch := script[i]
		consumed := 1

		next := byte(0)
		if i+1 < len(script) {
			next = script[i+1]
		}

		if inLineComment {
			if ch == '\n' || ch == '\r' {
				inLineComment = false
			}
		} else if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				consumed = 2
			}
		} else if inSingle {
			if ch == '\'' {
				if next == '\'' {
					consumed = 2
				} else {
					inSingle = false
				}
			}
		} else if inDouble {
			if ch == '"' {
				if next == '"' {
					consumed = 2
				} else {
					inDouble = false
				}
			}
		} else if inBacktick {
			if ch == '`' {
				if next == '`' {
					consumed = 2
				} else {
					inBacktick = false
				}
			}
		} else {
			switch {
			case ch == '-' && next == '-':
				inLineComment = true
				consumed = 2
			case ch == '/' && next == '*':
				inBlockComment = true
				consumed = 2
			case ch == '\'':
				inSingle = true
			case ch == '"':
				inDouble = true
			case ch == '`':
				inBacktick = true
			case ch == ';':
				segment := strings.TrimSpace(script[startIndex:i])
				if segment != "" {
					statements = append(statements, Statement{
						Text:  script[startIndex:i],
						Start: Position{Line: startLine, Column: startColumn, Offset: startOffset},
					})
				}
				startIndex = i + 1
				startLine, startColumn, startOffset = line, column+1, offset+1
			}
		}

		if ch == '\r' {
			if next == '\n' {
				consumed = max(consumed, 2)
			}
			line++
			column = 1
		} else if ch == '\n' {
			line++
			column = 1
		} else {
			column += consumed
		}

		i += consumed
		offset += consumed
	}

	tail := strings.TrimSpace(script[startIndex:])
	if tail != "" {
		statements = append(statements, Statement{
			Text:  script[startIndex:],
			Start: Position{Line: startLine, Column: startColumn, Offset: startOffset},
		})
	}

	return statements
}
