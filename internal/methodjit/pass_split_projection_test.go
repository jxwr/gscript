package methodjit

import (
	"strings"
	"testing"
)

func TestIntrinsic_StringSplitConstIndexProjection(t *testing.T) {
	proto := compile(t, `
func f(line) {
	parts := string.split(line, "|")
	return parts[2] .. parts[4]
}
`)
	fn := BuildGraph(proto)
	fn, notes := IntrinsicPass(fn)
	fn, err := DCEPass(fn)
	if err != nil {
		t.Fatalf("DCEPass: %v", err)
	}
	ir := Print(fn)
	if countOpHelper(fn, OpCall) != 0 {
		t.Fatalf("split call should be eliminated after projection\n%s", ir)
	}
	if countOpHelper(fn, OpGetTable) != 0 {
		t.Fatalf("constant split reads should become projections\n%s", ir)
	}
	if got := countOpHelper(fn, OpStringSplitPart); got != 2 {
		t.Fatalf("StringSplitPart count=%d, want 2\n%s", got, ir)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "string.split const-index projections") {
		t.Fatalf("missing split projection note: %#v", notes)
	}
}

func TestIntrinsic_StringSplitProjectionRejectsEscapesAndMutation(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "escape",
			src: `
func f(line) {
	parts := string.split(line, "|")
	return parts
}
`,
		},
		{
			name: "mutation",
			src: `
func f(line) {
	parts := string.split(line, "|")
	parts[2] = "x"
	return parts[2]
}
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fn := BuildGraph(compile(t, tc.src))
			fn, _ = IntrinsicPass(fn)
			ir := Print(fn)
			if countOpHelper(fn, OpStringSplitPart) != 0 {
				t.Fatalf("unsafe split use should not be projected\n%s", ir)
			}
			if countOpHelper(fn, OpCall) == 0 {
				t.Fatalf("split call should remain for unsafe use\n%s", ir)
			}
		})
	}
}
