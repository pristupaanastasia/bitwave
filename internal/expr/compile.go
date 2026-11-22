package expr

import (
	"fmt"
	"math"
	"strings"
)

// WaveSize is the number of points in the hand-drawn wavetable read by w(x).
const WaveSize = 256

// Env holds the variables visible to a formula. The engine updates the
// fields before every Eval call.
type Env struct {
	T float64    // sample counter at the formula sample rate
	P float64    // phase of the current note: advances by 256 per period
	F float64    // frequency of the current note in Hz, 0 on a rest
	G float64    // gate: 1 while a note is active, 0 on a rest
	S float64    // index of the current sequencer step
	E float64    // envelope: 1 at the note start, decays linearly to 0
	R float64    // formula sample rate in Hz
	N float64    // formula ticks since the current note started (for drums)
	K [4]float64 // knobs k1..k4 set from the UI, 0..1

	Wave *[WaveSize]float64 // drawn wave, values in 0..255

	rng uint64
}

// Seed resets the random() generator, so renders are reproducible.
func (e *Env) Seed(s uint64) {
	if s == 0 {
		s = 0x9E3779B97F4A7C15
	}
	e.rng = s
}

func (e *Env) random() float64 {
	if e.rng == 0 {
		e.Seed(0)
	}
	e.rng ^= e.rng << 13
	e.rng ^= e.rng >> 7
	e.rng ^= e.rng << 17
	return float64(e.rng>>11) / (1 << 53)
}

// Program is a compiled formula.
type Program struct {
	Source string
	fn     func(*Env) float64
}

// Eval evaluates the formula for the current state of env.
func (p *Program) Eval(env *Env) float64 { return p.fn(env) }

// Compile parses src and returns an executable program.
func Compile(src string) (*Program, error) {
	if strings.TrimSpace(src) == "" {
		return nil, &Error{0, "empty formula"}
	}
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	ps := &parser{toks: toks}
	n, err := ps.parseExpr()
	if err != nil {
		return nil, err
	}
	if tok := ps.peek(); tok.kind != tokEOF {
		return nil, &Error{tok.pos, fmt.Sprintf("unexpected %q", tok.text)}
	}
	if n.array != nil {
		return nil, &Error{0, "array literal must be indexed, e.g. [1,2,3][t>>12&3]"}
	}
	return &Program{Source: src, fn: n.fn}, nil
}

// node is a compiled sub-expression. Constant sub-trees are folded at
// compile time; array literals only exist until they are indexed.
type node struct {
	fn      func(*Env) float64
	isConst bool
	val     float64
	array   []node
	pos     int
}

func constNode(v float64, pos int) node {
	return node{fn: func(*Env) float64 { return v }, isConst: true, val: v, pos: pos}
}

type parser struct {
	toks []token
	i    int
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) next() token { t := p.toks[p.i]; p.i++; return t }

func (p *parser) isOp(op string) bool {
	t := p.peek()
	return t.kind == tokOp && t.text == op
}

func (p *parser) expect(op string) error {
	t := p.peek()
	if t.kind != tokOp || t.text != op {
		found := t.text
		if t.kind == tokEOF {
			found = "end of formula"
		}
		return &Error{t.pos, fmt.Sprintf("expected %q, found %q", op, found)}
	}
	p.i++
	return nil
}

func (p *parser) parseExpr() (node, error) { return p.parseTernary() }

func (p *parser) parseTernary() (node, error) {
	cond, err := p.parseBinary(0)
	if err != nil {
		return node{}, err
	}
	if !p.isOp("?") {
		return cond, nil
	}
	p.next()
	a, err := p.parseTernary()
	if err != nil {
		return node{}, err
	}
	if err := p.expect(":"); err != nil {
		return node{}, err
	}
	b, err := p.parseTernary()
	if err != nil {
		return node{}, err
	}
	if err := scalar(cond, a, b); err != nil {
		return node{}, err
	}
	if cond.isConst {
		if truthy(cond.val) {
			return a, nil
		}
		return b, nil
	}
	c, af, bf := cond.fn, a.fn, b.fn
	return node{fn: func(e *Env) float64 {
		if truthy(c(e)) {
			return af(e)
		}
		return bf(e)
	}, pos: cond.pos}, nil
}

// Binary operator precedence levels, lowest first (JavaScript order).
var levels = [][]string{
	{"||"},
	{"&&"},
	{"|"},
	{"^"},
	{"&"},
	{"==", "!="},
	{"<", ">", "<=", ">="},
	{"<<", ">>", ">>>"},
	{"+", "-"},
	{"*", "/", "%"},
}

func (p *parser) parseBinary(level int) (node, error) {
	if level == len(levels) {
		return p.parseUnary()
	}
	left, err := p.parseBinary(level + 1)
	if err != nil {
		return node{}, err
	}
	for {
		t := p.peek()
		if t.kind != tokOp || !contains(levels[level], t.text) {
			return left, nil
		}
		p.next()
		right, err := p.parseBinary(level + 1)
		if err != nil {
			return node{}, err
		}
		if err := scalar(left, right); err != nil {
			return node{}, err
		}
		left = binary(t.text, left, right)
	}
}

func (p *parser) parseUnary() (node, error) {
	t := p.peek()
	if t.kind == tokOp && (t.text == "-" || t.text == "+" || t.text == "!" || t.text == "~") {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return node{}, err
		}
		if err := scalar(x); err != nil {
			return node{}, err
		}
		var op func(float64) float64
		switch t.text {
		case "-":
			op = func(v float64) float64 { return -v }
		case "+":
			op = func(v float64) float64 { return v }
		case "!":
			op = func(v float64) float64 { return b2f(!truthy(v)) }
		case "~":
			op = func(v float64) float64 { return float64(^toInt32(v)) }
		}
		if x.isConst {
			return constNode(op(x.val), t.pos), nil
		}
		f := x.fn
		return node{fn: func(e *Env) float64 { return op(f(e)) }, pos: t.pos}, nil
	}
	return p.parsePower()
}

func (p *parser) parsePower() (node, error) {
	base, err := p.parsePostfix()
	if err != nil {
		return node{}, err
	}
	if !p.isOp("**") {
		return base, nil
	}
	p.next()
	exp, err := p.parseUnary() // right associative
	if err != nil {
		return node{}, err
	}
	if err := scalar(base, exp); err != nil {
		return node{}, err
	}
	return binary("**", base, exp), nil
}

func (p *parser) parsePostfix() (node, error) {
	n, err := p.parsePrimary()
	if err != nil {
		return node{}, err
	}
	for p.isOp("[") {
		open := p.next()
		idx, err := p.parseExpr()
		if err != nil {
			return node{}, err
		}
		if err := p.expect("]"); err != nil {
			return node{}, err
		}
		if n.array == nil {
			return node{}, &Error{open.pos, "only array literals can be indexed"}
		}
		if err := scalar(idx); err != nil {
			return node{}, err
		}
		n = index(n.array, idx)
	}
	return n, nil
}

func (p *parser) parsePrimary() (node, error) {
	t := p.next()
	switch t.kind {
	case tokNum:
		return constNode(t.num, t.pos), nil
	case tokIdent:
		if p.isOp("(") {
			p.next()
			var args []node
			if !p.isOp(")") {
				for {
					a, err := p.parseExpr()
					if err != nil {
						return node{}, err
					}
					if err := scalar(a); err != nil {
						return node{}, err
					}
					args = append(args, a)
					if !p.isOp(",") {
						break
					}
					p.next()
				}
			}
			if err := p.expect(")"); err != nil {
				return node{}, err
			}
			return call(t, args)
		}
		return variable(t)
	case tokOp:
		switch t.text {
		case "(":
			n, err := p.parseExpr()
			if err != nil {
				return node{}, err
			}
			return n, p.expect(")")
		case "[":
			var items []node
			for !p.isOp("]") {
				it, err := p.parseExpr()
				if err != nil {
					return node{}, err
				}
				if err := scalar(it); err != nil {
					return node{}, err
				}
				items = append(items, it)
				if !p.isOp(",") {
					break
				}
				p.next()
			}
			if err := p.expect("]"); err != nil {
				return node{}, err
			}
			if len(items) == 0 {
				return node{}, &Error{t.pos, "empty array"}
			}
			return node{array: items, pos: t.pos}, nil
		}
		return node{}, &Error{t.pos, fmt.Sprintf("unexpected %q", t.text)}
	}
	return node{}, &Error{t.pos, "unexpected end of formula"}
}

func scalar(ns ...node) error {
	for _, n := range ns {
		if n.array != nil {
			return &Error{n.pos, "array literal must be indexed, e.g. [1,2,3][t>>12&3]"}
		}
	}
	return nil
}

func variable(t token) (node, error) {
	switch t.text {
	case "t":
		return node{fn: func(e *Env) float64 { return e.T }, pos: t.pos}, nil
	case "p":
		return node{fn: func(e *Env) float64 { return e.P }, pos: t.pos}, nil
	case "f":
		return node{fn: func(e *Env) float64 { return e.F }, pos: t.pos}, nil
	case "g":
		return node{fn: func(e *Env) float64 { return e.G }, pos: t.pos}, nil
	case "s":
		return node{fn: func(e *Env) float64 { return e.S }, pos: t.pos}, nil
	case "e":
		return node{fn: func(e *Env) float64 { return e.E }, pos: t.pos}, nil
	case "r":
		return node{fn: func(e *Env) float64 { return e.R }, pos: t.pos}, nil
	case "n":
		return node{fn: func(e *Env) float64 { return e.N }, pos: t.pos}, nil
	case "k1", "k2", "k3", "k4":
		i := t.text[1] - '1'
		return node{fn: func(e *Env) float64 { return e.K[i] }, pos: t.pos}, nil
	case "PI":
		return constNode(math.Pi, t.pos), nil
	case "E":
		return constNode(math.E, t.pos), nil
	}
	return node{}, &Error{t.pos, fmt.Sprintf("unknown variable %q (use t p f g s e n r k1..k4)", t.text)}
}

var funcs1 = map[string]func(float64) float64{
	"sin": math.Sin, "cos": math.Cos, "tan": math.Tan,
	"asin": math.Asin, "acos": math.Acos, "atan": math.Atan,
	"abs": math.Abs, "floor": math.Floor, "ceil": math.Ceil,
	"round": func(x float64) float64 { return math.Floor(x + 0.5) }, // JS rounding
	"trunc": math.Trunc, "int": math.Trunc,
	"tanh": math.Tanh, // soft saturation, handy for drive
	"sqrt": math.Sqrt, "cbrt": math.Cbrt, "exp": math.Exp,
	"log": math.Log, "log2": math.Log2, "log10": math.Log10,
	"sign": func(x float64) float64 {
		switch {
		case x > 0:
			return 1
		case x < 0:
			return -1
		}
		return x
	},
}

var funcs2 = map[string]func(float64, float64) float64{
	"pow": math.Pow, "atan2": math.Atan2,
}

func call(t token, args []node) (node, error) {
	argErr := func(want string) (node, error) {
		return node{}, &Error{t.pos, fmt.Sprintf("%s() takes %s", t.text, want)}
	}
	if f, ok := funcs1[t.text]; ok {
		if len(args) != 1 {
			return argErr("1 argument")
		}
		a := args[0]
		if a.isConst {
			return constNode(f(a.val), t.pos), nil
		}
		af := a.fn
		return node{fn: func(e *Env) float64 { return f(af(e)) }, pos: t.pos}, nil
	}
	if f, ok := funcs2[t.text]; ok {
		if len(args) != 2 {
			return argErr("2 arguments")
		}
		a, b := args[0], args[1]
		if a.isConst && b.isConst {
			return constNode(f(a.val, b.val), t.pos), nil
		}
		af, bf := a.fn, b.fn
		return node{fn: func(e *Env) float64 { return f(af(e), bf(e)) }, pos: t.pos}, nil
	}
	switch t.text {
	case "min", "max":
		if len(args) == 0 {
			return argErr("at least 1 argument")
		}
		pick := math.Min
		if t.text == "max" {
			pick = math.Max
		}
		fs := make([]func(*Env) float64, len(args))
		for i, a := range args {
			fs[i] = a.fn
		}
		return node{fn: func(e *Env) float64 {
			v := fs[0](e)
			for _, f := range fs[1:] {
				v = pick(v, f(e))
			}
			return v
		}, pos: t.pos}, nil
	case "random":
		if len(args) != 0 {
			return argErr("no arguments")
		}
		return node{fn: func(e *Env) float64 { return e.random() }, pos: t.pos}, nil
	case "w":
		if len(args) != 1 {
			return argErr("1 argument")
		}
		af := args[0].fn
		return node{fn: func(e *Env) float64 {
			if e.Wave == nil {
				return 128
			}
			return e.Wave[toInt32(af(e))&(WaveSize-1)]
		}, pos: t.pos}, nil
	}
	return node{}, &Error{t.pos, fmt.Sprintf("unknown function %q", t.text)}
}

// index implements JavaScript array indexing: an index that is fractional,
// negative or past the end yields undefined, which is NaN as a number (and 0
// once it passes through a bitwise operator).
func index(items []node, idx node) node {
	fs := make([]func(*Env) float64, len(items))
	for i, it := range items {
		fs[i] = it.fn
	}
	n := float64(len(fs))
	valid := func(v float64) bool { return v >= 0 && v < n && v == math.Trunc(v) }
	if idx.isConst {
		if !valid(idx.val) {
			return constNode(math.NaN(), idx.pos)
		}
		return items[int(idx.val)]
	}
	ix := idx.fn
	return node{fn: func(e *Env) float64 {
		v := ix(e)
		if !valid(v) {
			return math.NaN()
		}
		return fs[int(v)](e)
	}, pos: idx.pos}
}

func binary(op string, l, r node) node {
	var f func(a, b float64) float64
	switch op {
	case "+":
		f = func(a, b float64) float64 { return a + b }
	case "-":
		f = func(a, b float64) float64 { return a - b }
	case "*":
		f = func(a, b float64) float64 { return a * b }
	case "/":
		f = func(a, b float64) float64 { return a / b }
	case "%":
		f = math.Mod
	case "**":
		f = math.Pow
	case "&":
		f = func(a, b float64) float64 { return float64(toInt32(a) & toInt32(b)) }
	case "|":
		f = func(a, b float64) float64 { return float64(toInt32(a) | toInt32(b)) }
	case "^":
		f = func(a, b float64) float64 { return float64(toInt32(a) ^ toInt32(b)) }
	case "<<":
		f = func(a, b float64) float64 { return float64(toInt32(a) << (uint32(toInt32(b)) & 31)) }
	case ">>":
		f = func(a, b float64) float64 { return float64(toInt32(a) >> (uint32(toInt32(b)) & 31)) }
	case ">>>":
		f = func(a, b float64) float64 { return float64(uint32(toInt32(a)) >> (uint32(toInt32(b)) & 31)) }
	case "<":
		f = func(a, b float64) float64 { return b2f(a < b) }
	case ">":
		f = func(a, b float64) float64 { return b2f(a > b) }
	case "<=":
		f = func(a, b float64) float64 { return b2f(a <= b) }
	case ">=":
		f = func(a, b float64) float64 { return b2f(a >= b) }
	case "==":
		f = func(a, b float64) float64 { return b2f(a == b) }
	case "!=":
		f = func(a, b float64) float64 { return b2f(a != b) }
	case "&&", "||":
		// Short-circuit and return an operand, like JavaScript.
		and := op == "&&"
		if l.isConst && r.isConst {
			if truthy(l.val) == and {
				return r
			}
			return l
		}
		lf, rf := l.fn, r.fn
		return node{fn: func(e *Env) float64 {
			a := lf(e)
			if truthy(a) == and {
				return rf(e)
			}
			return a
		}, pos: l.pos}
	}
	if l.isConst && r.isConst {
		return constNode(f(l.val, r.val), l.pos)
	}
	lf, rf := l.fn, r.fn
	switch {
	case r.isConst: // very common: t>>8, t&255, t*3
		c := r.val
		return node{fn: func(e *Env) float64 { return f(lf(e), c) }, pos: l.pos}
	case l.isConst:
		c := l.val
		return node{fn: func(e *Env) float64 { return f(c, rf(e)) }, pos: l.pos}
	}
	return node{fn: func(e *Env) float64 { return f(lf(e), rf(e)) }, pos: l.pos}
}

// toInt32 implements the JavaScript ToInt32 conversion used by bitwise ops.
func toInt32(x float64) int32 {
	if x >= math.MinInt32 && x <= math.MaxInt32 {
		return int32(x)
	}
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	m := math.Mod(math.Trunc(x), 1<<32)
	if m < 0 {
		m += 1 << 32
	}
	return int32(uint32(m))
}

// ToInt32 is exported for the audio engine's byte output mode.
func ToInt32(x float64) int32 { return toInt32(x) }

func truthy(v float64) bool { return v != 0 && !math.IsNaN(v) }

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
