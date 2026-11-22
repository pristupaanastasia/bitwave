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
	env := &Env{T: 1000, P: 300, F: 440, G: 1, S: 3, E: 0.5, R: 8000, N: 7}
	cases := []struct {
		src  string
		want float64
	}{
		{"1+2*3", 7},
		{"(1+2)*3", 9},
		{"t", 1000},
		{"t>>3", 125},
		{"t&255", 1000 & 255},
		{"t|7", 1000 | 7},
		{"t^255", 1000 ^ 255},
		{"~0", -1},
		{"-1>>>28", 15},
		{"-8>>1", -4},
		{"1<<33", 2},        // shift count masked by 31
		{"4294967296|0", 0}, // ToInt32 wraps
		{"2147483648|0", -2147483648},
		{"7/2", 3.5},
		{"-7%3", -1},
		{"2**3**2", 512},
		{"-2**2", -4},
		{"1<2", 1},
		{"1==2", 0},
		{"t>500?1:2", 1},
		{"0?1:0?2:3", 3},
		{"0||5", 5},
		{"3&&4", 4},
		{"0&&4", 0},
		{"!0", 1},
		{"[10,20,30][1]", 20},
		{"[10,20,30][2]", 30},
		{"[10,20,30][4]|0", 0}, // out of range is undefined (NaN) as in JS
		{"[10,20,30][-1]|0", 0},
		{"[10,20,30][1.5]|0", 0},
		{"[1,2][t>>9&1]", 2},
		{"Math.floor(3.7)", 3},
		{"round(2.5)", 3},
		{"min(4,2,8)", 2},
		{"max(4,2,8)", 8},
		{"pow(2,10)", 1024},
		{"0xff", 255},
		{"1e3", 1000},
		{".5*4", 2},
		{"p+f+g+s+e+r+n", 300 + 440 + 1 + 3 + 0.5 + 8000 + 7},
		{"(t*(t>>5|t>>8))>>(t>>16)", float64((1000 * (1000>>5 | 1000>>8)) >> 0)},
	}
	for _, c := range cases {
		if got := eval(t, c.src, env); got != c.want {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}
}

func TestDivByZeroIsSafeInBitOps(t *testing.T) {
	if got := eval(t, "(1/0)|0", &Env{}); got != 0 {
		t.Errorf("got %v", got)
	}
	if got := eval(t, "1/0", &Env{}); !math.IsInf(got, 1) {
		t.Errorf("got %v", got)
	}
}

func TestWave(t *testing.T) {
	var w [WaveSize]float64
	for i := range w {
		w[i] = float64(i)
	}
	env := &Env{Wave: &w}
	if got := eval(t, "w(258)", env); got != 2 {
		t.Errorf("w(258) = %v", got)
	}
	if got := eval(t, "w(-1)", env); got != 255 {
		t.Errorf("w(-1) = %v", got)
	}
}

func TestRandomDeterministic(t *testing.T) {
	a, b := &Env{}, &Env{}
	a.Seed(42)
	b.Seed(42)
	p, _ := Compile("random()")
	for range 10 {
		x, y := p.Eval(a), p.Eval(b)
		if x != y || x < 0 || x >= 1 {
			t.Fatalf("random mismatch %v %v", x, y)
		}
	}
}

func TestErrors(t *testing.T) {
	for _, src := range []string{
		"т&т>>8", "t\u00a0&255", "t\u2212 1", "t\u00d72", "\xff",
		"", "1+", "(1", "t>>", "foo", "bar(1)", "sin(1,2)", "[1,2]", "t[1]", "1 $ 2", "1?2", "[]",
	} {
		if _, err := Compile(src); err == nil {
			t.Errorf("Compile(%q) succeeded, want error", src)
		}
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

func TestFunctionsAndUnary(t *testing.T) {
	env := &Env{T: 4}
	cases := map[string]float64{
		"+t": 4, "-t": -4, "~t": -5, "!t": 0, "--t": 4,
		"sin(0)": 0, "cos(0)": 1, "tan(0)": 0, "asin(0)": 0, "acos(1)": 0, "atan(0)": 0,
		"abs(-t)": 4, "ceil(1.2)": 2, "trunc(-1.7)": -1, "int(t/3)": 1,
		"sqrt(t)": 2, "cbrt(27)": 3, "exp(0)": 1, "log(1)": 0, "log2(t)": 2, "log10(100)": 2,
		"sign(-t)": -1, "sign(t)": 1, "sign(0)": 0, "atan2(0,1)": 0,
		"min(t)": 4, "max(t,9,2)": 9, "sin(t)*0": 0, "pow(t,2)": 16, "pow(2,t)": 16,
		"t%3": 1, "t-1": 3, "t>=4": 1, "t<=3": 0, "t!=4": 0, "t==4": 1, "t>4": 0,
		"t>>>1": 2, "t<<2": 16, "t**2": 16, "1<<t": 16, "8>>t": 0,
		"t?t:0": 4, "t&&0": 0, "0||t": 4, "t||9": 4, "E>2": 1, "PI>3": 1,
		"[t,2][0]": 4, "[1,2][t-4]": 1, "[1,2][t]|0": 0,
	}
	for src, want := range cases {
		if got := eval(t, src, env); math.Abs(got-want) > 1e-12 {
			t.Errorf("%s = %v, want %v", src, got, want)
		}
	}
}

func TestKnobsAndTanh(t *testing.T) {
	env := &Env{K: [4]float64{0.1, 0.2, 0.3, 0.4}}
	if got := eval(t, "k1+k2*10+k3*100+k4*1000", env); math.Abs(got-(0.1+2+30+400)) > 1e-9 {
		t.Fatal(got)
	}
	if got := eval(t, "tanh(100)", env); got != 1 {
		t.Fatal(got)
	}
	if _, err := Compile("k5"); err == nil {
		t.Fatal("k5 must not exist")
	}
}

func TestErrorMessage(t *testing.T) {
	_, err := Compile("t + foo")
	if err == nil || err.Error() != `col 5: unknown variable "foo" (use t p f g s e n r k1..k4)` {
		t.Fatalf("%v", err)
	}
	for _, src := range []string{"min()", "random(1)", "w()", "pow(1)", "0x", "1.2.3", "-[1,2]", "[1,2]+1", "sin([1])", "[[1]]", "(1)[0]", "t ? [1] : 2"} {
		if _, err := Compile(src); err == nil {
			t.Errorf("Compile(%q) succeeded, want error", src)
		}
	}
}

func TestToInt32Exported(t *testing.T) {
	if ToInt32(-1.9) != -1 || ToInt32(math.NaN()) != 0 || ToInt32(math.Inf(1)) != 0 || ToInt32(-4294967297) != -1 {
		t.Fatal("ToInt32")
	}
}

func TestSeedZeroAndWaveNil(t *testing.T) {
	var e Env
	e.Seed(0)
	if v := eval(t, "random()", &e); v < 0 || v >= 1 {
		t.Fatal(v)
	}
	if eval(t, "w(3)", &Env{}) != 128 {
		t.Fatal("nil wave should read as silence")
	}
}
