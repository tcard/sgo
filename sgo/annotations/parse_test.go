package annotations

import "testing"

func TestParse(t *testing.T) {
	type testCase struct {
		input  string
		output map[string]string
	}
	cases := []testCase{
		{
			input: "  foo  xyz  ;  ( *  bar ) {  ab c \n qux { ñandú poqe{ñ..asd(oan); }\n } \n ",
			output: map[string]string{
				"foo":              "xyz",
				"(*bar).ab":        "c",
				"(*bar).qux.ñandú": "poqe{ñ..asd(oan)",
			},
		},
	}
	for i, c := range cases {
		anns, err := parseList(NewTokenizer(c.input))
		if err != nil {
			t.Errorf("case %d: unexpected error: %v", i, err)
		} else if !mapEqual(c.output, anns) {
			t.Errorf("case %d: expected %v, got %v", i, c.output, anns)
		}
	}
}

func mapEqual(a, b map[string]string) bool {
	if (a == nil && b != nil) || (b == nil && a != nil) {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		if vb, ok := b[k]; !ok || va != vb {
			return false
		}
	}
	return true
}
