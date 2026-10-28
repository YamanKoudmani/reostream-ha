package control

import (
	"strings"
	"testing"
)

// The page tells an operator that the quickest way to know what to search is
// the address of a camera they already have. Refusing that address for
// missing a "/24" is the page contradicting its own advice, so a bare
// address means the /24 around it.
func TestABareAddressMeansItsOwnSlash24(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"192.168.1.50", "192.168.1.0/24"},
		{"10.0.0.7", "10.0.0.0/24"},
		{"  192.168.4.9  ", "192.168.4.0/24"},
	} {
		p, err := parseSweepRange(c.in)
		if err != nil {
			t.Errorf("%q was refused: %v", c.in, err)
			continue
		}
		if p.String() != c.want {
			t.Errorf("%q became %s, want %s", c.in, p, c.want)
		}
	}

	// A bare address outside RFC1918 is still just the /24 around it, and
	// is accepted like any other: camera VLANs are routinely built on such
	// space, and there is no longer an ownership confirmation to clear.
	p, err := parseSweepRange("192.0.2.11")
	if err != nil {
		t.Fatalf("a bare non-private address was refused: %v", err)
	}
	if p.String() != "192.0.2.0/24" {
		t.Errorf("bare address became %s, want 192.0.2.0/24", p)
	}
}

// Nonsense still has to be refused, and the refusal has to describe both
// things the field now accepts.
func TestSomethingThatIsNeitherAnAddressNorARangeIsRefused(t *testing.T) {
	for _, in := range []string{"", "camera", "192.168.1", "192.168.1.0/", "::1"} {
		if _, err := parseSweepRange(in); err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
	_, err := parseSweepRange("camera")
	if err == nil || !strings.Contains(err.Error(), "address or a range") {
		t.Errorf("the refusal does not say what the field takes: %v", err)
	}
}
