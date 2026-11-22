package expr

import (
	"math"
	"testing"
)

func eval(t *testing.T, src string, env *Env) float64 {
	t.Helper()
	p, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile(%q): %v", src, err)
	}
	return p.Eval(env)
}

func TestEval(t *testing.T) {
	vars := &Env{T: 1000, P: 300, F: 440, G: 1, S: 3, E: 0.5, R: 8000, N: 7}
	four := &Env{T: 4}
	knobs := &Env{K: [4]float64{0.1, 0.2, 0.3, 0.4}}
	var ramp [WaveSize]float64
	for i := range ramp {
		ramp[i] = float64(i)
	}
	wave := &Env{Wave: &ramp}

	tests := []struct {
		src  string
		env  *Env
		want float64
	}{
		// Arithmetic and precedence.
		{"1+2*3", vars, 7},
		{"(1+2)*3", vars, 9},
		{"7/2", vars, 3.5},
		{"-7%3", vars, -1},
		{"t%3", four, 1},
		{"t-1", four, 3},
		{"2**3**2", vars, 512},
		{"-2**2", vars, -4},
		{"t**2", four, 16},
		{"1/0", vars, math.Inf(1)},

		// Bitwise operators with JavaScript ToInt32 semantics.
		{"t>>3", vars, 125},
		{"t&255", vars, 1000 & 255},
		{"t|7", vars, 1000 | 7},
		{"t^255", vars, 1000 ^ 255},
		{"~0", vars, -1},
		{"-1>>>28", vars, 15},
		{"-8>>1", vars, -4},
		{"1<<33", vars, 2},        // shift count masked by 31
		{"4294967296|0", vars, 0}, // ToInt32 wraps
		{"2147483648|0", vars, -2147483648},
		{"(1/0)|0", vars, 0}, // Infinity is 0 in bit ops
		{"t>>>1", four, 2},
		{"t<<2", four, 16},
		{"1<<t", four, 16},
		{"8>>t", four, 0},
		{"(t*(t>>5|t>>8))>>(t>>16)", vars, float64((1000 * (1000>>5 | 1000>>8)) >> 0)},

		// Unary operators.
		{"+t", four, 4},
		{"-t", four, -4},
		{"~t", four, -5},
		{"!t", four, 0},
		{"!0", vars, 1},
		{"--t", four, 4},

		// Comparisons, logic and the ternary.
		{"1<2", vars, 1},
		{"1==2", vars, 0},
		{"t>=4", four, 1},
		{"t<=3", four, 0},
		{"t!=4", four, 0},
		{"t==4", four, 1},
		{"t>4", four, 0},
		{"t>500?1:2", vars, 1},
		{"0?1:0?2:3", vars, 3},
		{"t?t:0", four, 4},
		{"0||5", vars, 5},
		{"3&&4", vars, 4},
		{"0&&4", vars, 0},
		{"t&&0", four, 0},
		{"0||t", four, 4},
		{"t||9", four, 4},

		// Arrays: out of range or fractional indices are undefined (NaN) as in JS.
		{"[10,20,30][1]", vars, 20},
		{"[10,20,30][2]", vars, 30},
		{"[10,20,30][4]|0", vars, 0},
		{"[10,20,30][-1]|0", vars, 0},
		{"[10,20,30][1.5]|0", vars, 0},
		{"[1,2][t>>9&1]", vars, 2},
		{"[t,2][0]", four, 4},
		{"[1,2][t-4]", four, 1},
		{"[1,2][t]|0", four, 0},

		// Number literals and constants.
		{"0xff", vars, 255},
		{"1e3", vars, 1000},
		{".5*4", vars, 2},
		{"E>2", four, 1},
		{"PI>3", four, 1},

		// Functions.
		{"Math.floor(3.7)", vars, 3},
		{"round(2.5)", vars, 3},
		{"min(4,2,8)", vars, 2},
		{"max(4,2,8)", vars, 8},
		{"min(t)", four, 4},
		{"max(t,9,2)", four, 9},
		{"pow(2,10)", vars, 1024},
		{"pow(t,2)", four, 16},
		{"pow(2,t)", four, 16},
		{"sin(0)", four, 0},
		{"cos(0)", four, 1},
		{"tan(0)", four, 0},
		{"asin(0)", four, 0},
		{"acos(1)", four, 0},
		{"atan(0)", four, 0},
		{"atan2(0,1)", four, 0},
		{"sin(t)*0", four, 0},
		{"tanh(100)", four, 1},
		{"abs(-t)", four, 4},
		{"ceil(1.2)", four, 2},
		{"trunc(-1.7)", four, -1},
		{"int(t/3)", four, 1},
		{"sqrt(t)", four, 2},
		{"cbrt(27)", four, 3},
		{"exp(0)", four, 1},
		{"log(1)", four, 0},
		{"log2(t)", four, 2},
		{"log10(100)", four, 2},
		{"sign(-t)", four, -1},
		{"sign(t)", four, 1},
		{"sign(0)", four, 0},

		// Variables.
		{"t", vars, 1000},
		{"p+f+g+s+e+r+n", vars, 300 + 440 + 1 + 3 + 0.5 + 8000 + 7},
		{"k1+k2*10+k3*100+k4*1000", knobs, 0.1 + 2 + 30 + 400},

		// The drawn wave: index wraps to 0..255, a missing wave is silence.
		{"w(258)", wave, 2},
		{"w(-1)", wave, 255},
		{"w(3)", &Env{}, 128},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			got := eval(t, tt.src, tt.env)
			if got != tt.want && math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("= %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompileErrors(t *testing.T) {
	tests := []struct {
		name, src string
		wantMsg   string // checked when not empty
	}{
		{"message names the column", "t + foo", `col 5: unknown variable "foo" (use t p f g s e n r k1..k4)`},
		{"cyrillic letter", "т&т>>8", ""},
		{"no-break space", "t &255", ""},
		{"typographic minus", "t− 1", ""},
		{"multiplication sign", "t×2", ""},
		{"invalid utf-8", "\xff", ""},
		{"empty", "", ""},
		{"dangling operator", "1+", ""},
		{"unclosed paren", "(1", ""},
		{"dangling shift", "t>>", ""},
		{"unknown variable", "foo", ""},
		{"no such knob", "k5", ""},
		{"unknown function", "bar(1)", ""},
		{"too many args", "sin(1,2)", ""},
		{"min without args", "min()", ""},
		{"random with args", "random(1)", ""},
		{"w without args", "w()", ""},
		{"pow with one arg", "pow(1)", ""},
		{"bad hex", "0x", ""},
		{"bad number", "1.2.3", ""},
		{"unknown character", "1 $ 2", ""},
		{"ternary without else", "1?2", ""},
		{"empty array", "[]", ""},
		{"unindexed array", "[1,2]", ""},
		{"negated array", "-[1,2]", ""},
		{"array in arithmetic", "[1,2]+1", ""},
		{"array as argument", "sin([1])", ""},
		{"nested array", "[[1]]", ""},
		{"indexing a variable", "t[1]", ""},
		{"indexing a paren", "(1)[0]", ""},
		{"array in ternary", "t ? [1] : 2", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Compile(tt.src)
			switch {
			case err == nil:
				t.Fatalf("Compile(%q) succeeded, want error", tt.src)
			case tt.wantMsg != "" && err.Error() != tt.wantMsg:
				t.Errorf("message %q, want %q", err.Error(), tt.wantMsg)
			}
		})
	}
}

func TestToInt32(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want int32
	}{
		{"truncates toward zero", -1.9, -1},
		{"NaN", math.NaN(), 0},
		{"infinity", math.Inf(1), 0},
		{"wraps below int32", -4294967297, -1},
		{"wraps above int32", 4294967297, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToInt32(tt.in); got != tt.want {
				t.Errorf("ToInt32(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestRandom(t *testing.T) {
	p, _ := Compile("random()")
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"same seed, same sequence in 0..1", func(t *testing.T) {
			a, b := &Env{}, &Env{}
			a.Seed(42)
			b.Seed(42)
			for range 10 {
				if x, y := p.Eval(a), p.Eval(b); x != y || x < 0 || x >= 1 {
					t.Fatalf("random mismatch %v %v", x, y)
				}
			}
		}},
		{"seed 0 still produces numbers", func(t *testing.T) {
			var e Env
			e.Seed(0)
			if v := p.Eval(&e); v < 0 || v >= 1 {
				t.Fatal(v)
			}
		}},
		{"unseeded env works", func(t *testing.T) {
			if v := p.Eval(&Env{}); v < 0 || v >= 1 {
				t.Fatal(v)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

func BenchmarkClassic(b *testing.B) {
	p, _ := Compile("(t*(t>>5|t>>8))>>(t>>16)&255")
	env := &Env{}
	for i := 0; b.Loop(); i++ {
		env.T = float64(i)
		p.Eval(env)
	}
}
