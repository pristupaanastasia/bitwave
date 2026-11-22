// Package expr parses and compiles bytebeat-style formulas.
//
// The language follows JavaScript expression semantics, because that is what
// virtually every published bytebeat formula is written in: numbers are
// float64, bitwise operators convert their operands to int32 first, and
// comparisons yield 1 or 0.
package expr

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokNum
	tokIdent
	tokOp
)

type token struct {
	kind tokenKind
	text string
	num  float64
	pos  int
}

// Error is a parse error with the byte offset where it happened.
type Error struct {
	Pos int
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("col %d: %s", e.Pos+1, e.Msg) }

// Operators ordered longest first so the lexer is greedy.
var operators = []string{
	">>>", "<<", ">>", "<=", ">=", "==", "!=", "&&", "||", "**",
	"+", "-", "*", "/", "%", "&", "|", "^", "~", "!", "<", ">",
	"?", ":", "(", ")", "[", "]", ",",
}

func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := rune(src[i])
		switch {
		case unicode.IsSpace(c):
			i++
		case c >= '0' && c <= '9' || c == '.' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9':
			start := i
			if c == '0' && i+1 < len(src) && (src[i+1] == 'x' || src[i+1] == 'X') {
				i += 2
				for i < len(src) && isHex(src[i]) {
					i++
				}
				v, err := strconv.ParseUint(src[start+2:i], 16, 64)
				if err != nil {
					return nil, &Error{start, "bad hex number"}
				}
				toks = append(toks, token{kind: tokNum, num: float64(v), pos: start, text: src[start:i]})
				continue
			}
			for i < len(src) && (src[i] >= '0' && src[i] <= '9' || src[i] == '.') {
				i++
			}
			if i < len(src) && (src[i] == 'e' || src[i] == 'E') {
				j := i + 1
				if j < len(src) && (src[j] == '+' || src[j] == '-') {
					j++
				}
				if j < len(src) && src[j] >= '0' && src[j] <= '9' {
					i = j
					for i < len(src) && src[i] >= '0' && src[i] <= '9' {
						i++
					}
				}
			}
			v, err := strconv.ParseFloat(src[start:i], 64)
			if err != nil {
				return nil, &Error{start, "bad number " + strconv.Quote(src[start:i])}
			}
			toks = append(toks, token{kind: tokNum, num: v, pos: start, text: src[start:i]})
		case c == '_' || c < 0x80 && unicode.IsLetter(c):
			start := i
			for i < len(src) && (src[i] == '_' || src[i] == '.' || isAlnum(src[i])) {
				i++
			}
			// Allow "Math.sin" style names by dropping the "Math." prefix.
			name := strings.TrimPrefix(src[start:i], "Math.")
			toks = append(toks, token{kind: tokIdent, text: name, pos: start})
		default:
			matched := false
			for _, op := range operators {
				if strings.HasPrefix(src[i:], op) {
					toks = append(toks, token{kind: tokOp, text: op, pos: i})
					i += len(op)
					matched = true
					break
				}
			}
			if !matched {
				// Decode the whole rune: a pasted NBSP or typographic minus
				// should be named in the error, not shown as a stray byte.
				r, _ := utf8.DecodeRuneInString(src[i:])
				return nil, &Error{i, fmt.Sprintf("unexpected character %q", r)}
			}
		}
	}
	toks = append(toks, token{kind: tokEOF, pos: len(src)})
	return toks, nil
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func isAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
