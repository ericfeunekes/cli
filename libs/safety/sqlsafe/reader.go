package sqlsafe

import (
	"errors"
)

type reader struct {
	text   string
	index  int
	line   int
	column int
	offset int
	depth  int
}

func newReader(stmt Statement) *reader {
	return &reader{
		text:   stmt.Text,
		index:  0,
		line:   stmt.Start.Line,
		column: stmt.Start.Column,
		offset: stmt.Start.Offset,
	}
}

func (r *reader) eof() bool {
	return r.index >= len(r.text)
}

func (r *reader) peek() byte {
	if r.eof() {
		return 0
	}
	return r.text[r.index]
}

func (r *reader) peekNext() byte {
	if r.index+1 >= len(r.text) {
		return 0
	}
	return r.text[r.index+1]
}

func (r *reader) position() Position {
	return Position{Line: r.line, Column: r.column, Offset: r.offset}
}

func (r *reader) advance() byte {
	if r.eof() {
		return 0
	}
	ch := r.text[r.index]
	r.index++
	r.offset++

	if ch == '\r' {
		if r.index < len(r.text) && r.text[r.index] == '\n' {
			r.index++
			r.offset++
		}
		r.line++
		r.column = 1
		return '\n'
	}

	if ch == '\n' {
		r.line++
		r.column = 1
		return ch
	}

	r.column++
	return ch
}

func (r *reader) skipWhitespaceAndComments() {
	for !r.eof() {
		ch := r.peek()
		switch ch {
		case ' ', '\t', '\n', '\r', '\f':
			r.advance()
			continue
		case '-':
			if r.peekNext() == '-' {
				r.advance()
				r.advance()
				for !r.eof() {
					if r.advance() == '\n' {
						break
					}
				}
				continue
			}
		case '/':
			if r.peekNext() == '*' {
				r.advance()
				r.advance()
				for !r.eof() {
					ch = r.advance()
					if ch == '*' && r.peek() == '/' {
						r.advance()
						break
					}
				}
				continue
			}
		}
		break
	}
}

func (r *reader) skipQuoted(quote byte) {
	r.advance()
	for !r.eof() {
		ch := r.peek()
		if ch == quote {
			if r.peekNext() == quote {
				r.advance()
				r.advance()
				continue
			}
			r.advance()
			return
		}
		r.advance()
	}
}

func (r *reader) readIdentifier() string {
	start := r.index
	for !r.eof() {
		ch := r.peek()
		if isIdentifierPart(ch) {
			r.advance()
			continue
		}
		break
	}
	return r.text[start:r.index]
}

func isIdentifierStart(ch byte) bool {
	return ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
}

func isIdentifierPart(ch byte) bool {
	return isIdentifierStart(ch) || (ch >= '0' && ch <= '9')
}

func (r *reader) nextToken() (token string, pos Position, depth int, ok bool) {
	for !r.eof() {
		r.skipWhitespaceAndComments()
		if r.eof() {
			return "", Position{}, r.depth, false
		}
		ch := r.peek()
		switch ch {
		case '\'', '"', '`':
			r.skipQuoted(ch)
			continue
		case '(':
			r.depth++
			r.advance()
			continue
		case ')':
			if r.depth > 0 {
				r.depth--
			}
			r.advance()
			continue
		default:
			if isIdentifierStart(ch) {
				pos = r.position()
				token = r.readIdentifier()
				return token, pos, r.depth, true
			}
			r.advance()
		}
	}
	return "", Position{}, r.depth, false
}

var errUnexpectedEOF = errors.New("unexpected EOF")
