package httpheader

import "testing"

func TestValid(t *testing.T) {
	for _, n := range []string{"X-Foo", "content-type", "X_1!#$%&'*+.^`|~"} {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false", n)
		}
	}
	for _, n := range []string{"", "X Foo", "X:Foo", "X\nFoo", "X-Föö", "(x)"} {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true", n)
		}
	}
	for _, v := range []string{"", "a b", "tab\there", "ünïcode"} {
		if !ValidValue(v) {
			t.Errorf("ValidValue(%q) = false", v)
		}
	}
	for _, v := range []string{"a\nb", "a\r\nX-Injected: 1", "nul\x00", "del\x7f"} {
		if ValidValue(v) {
			t.Errorf("ValidValue(%q) = true", v)
		}
	}
}
