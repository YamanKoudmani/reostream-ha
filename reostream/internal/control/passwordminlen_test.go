package control

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The form's minlength must be the rule the server enforces.
//
// It was not. password.html carried a hardcoded minlength="16" and a hint
// saying "At least 16 characters", and both went on saying 16 after the
// floor moved to 12 -- so the browser refused a password the server would
// have accepted, and the only sign was a box that would not submit. The
// claim page had been passing the constant through for exactly this reason,
// with a comment saying why; this page had not.
//
// Asserting the number appears NOWHERE as a literal is what makes this
// durable: a future move of the floor breaks the test if either place is
// left behind.
func TestThePasswordFormAdvertisesTheRuleTheServerEnforces(t *testing.T) {
	// A CLAIMED install: an unclaimed one sends every route to the claim
	// screen, so there would be no password page to look at.
	path := filepath.Join(t.TempDir(), "config.toml")
	raw := "listen = \"0.0.0.0:8560\"\n\n[control]\nlisten = \"0.0.0.0:8562\"\nallow_no_password = true\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, Options{AllowNoPassword: true, ConfigPath: path})

	req := httptest.NewRequest("GET", "/password", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /password answered %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	want := fmt.Sprintf(`minlength="%d"`, claimPasswordMinLength)
	if !strings.Contains(body, want) {
		t.Errorf("the form does not carry %s", want)
	}
	if !strings.Contains(body, fmt.Sprintf("At least %d characters", claimPasswordMinLength)) {
		t.Errorf("the hint does not state the %d character rule", claimPasswordMinLength)
	}

	// And no other length is advertised. 16 was the old floor and is the
	// value that was left behind; any stale number is the same bug.
	for _, stale := range []int{8, 10, 14, 16, 20} {
		if stale == claimPasswordMinLength {
			continue
		}
		for _, form := range []string{
			fmt.Sprintf(`minlength="%d"`, stale),
			fmt.Sprintf("At least %d characters", stale),
		} {
			if strings.Contains(body, form) {
				t.Errorf("the page still advertises %q; the rule is %d",
					form, claimPasswordMinLength)
			}
		}
	}
}
