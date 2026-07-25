package validate

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestRules(t *testing.T) {
	digits := regexp.MustCompile(`^\d+$`)

	tests := []struct {
		name    string
		rule    Rule
		value   string
		wantMsg string // "" means the value must pass
	}{
		{"required rejects empty", Required, "", "can't be blank"},
		{"required rejects whitespace only", Required, "   \t\n ", "can't be blank"},
		{"required accepts content", Required, "a", ""},
		{"required keeps surrounding space", Required, "  a  ", ""},

		{"email accepts plain address", Email, "user@example.com", ""},
		{"email accepts display name form", Email, "Amy <amy@example.com>", ""},
		{"email rejects missing host", Email, "user@", "must be a valid email"},
		{"email rejects bare word", Email, "user", "must be a valid email"},

		{"int accepts positive", Int, "42", ""},
		{"int accepts negative", Int, "-7", ""},
		{"int rejects decimal", Int, "1.5", "must be a number"},
		{"int rejects word", Int, "twelve", "must be a number"},

		{"minlen counts runes not bytes", MinLen(3), "中文字", ""},
		{"minlen rejects short", MinLen(3), "ab", "must be at least 3 characters"},
		{"minlen accepts exact", MinLen(3), "abc", ""},

		{"maxlen counts runes not bytes", MaxLen(3), "中文字", ""},
		{"maxlen rejects long", MaxLen(3), "abcd", "must be at most 3 characters"},
		{"maxlen accepts exact", MaxLen(3), "abc", ""},

		{"match accepts", Match(digits), "123", ""},
		{"match rejects", Match(digits), "12a", "is not valid"},

		{"in accepts member", In("draft", "live"), "live", ""},
		{"in rejects non-member", In("draft", "live"), "gone", "must be one of: draft, live"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rule(tt.value)
			switch {
			case tt.wantMsg == "" && err != nil:
				t.Fatalf("want pass, got error %q", err)
			case tt.wantMsg != "" && err == nil:
				t.Fatalf("want error %q, got pass", tt.wantMsg)
			case tt.wantMsg != "" && err.Error() != tt.wantMsg:
				t.Fatalf("message: want %q, got %q", tt.wantMsg, err.Error())
			}
		})
	}
}

// Every rule but Required lets a blank value through, which is what makes an
// optional field a bare rule list instead of an `if value != ""` in the handler.
func TestBlankPassesEveryRuleButRequired(t *testing.T) {
	rules := map[string]Rule{
		"Email":  Email,
		"Int":    Int,
		"MinLen": MinLen(3),
		"MaxLen": MaxLen(3),
		"Match":  Match(regexp.MustCompile(`^\d+$`)),
		"In":     In("draft", "live"),
	}

	for name, rule := range rules {
		for _, value := range []string{"", "   "} {
			if err := rule(value); err != nil {
				t.Errorf("%s(%q): want pass, got %q", name, value, err)
			}
		}
	}

	if err := Required(""); err == nil {
		t.Error("Required(\"\"): want error, got pass")
	}
}

func TestMsgOverridesBuiltinAndCustomRules(t *testing.T) {
	if err := Msg(Required, "请填写名称")(""); err == nil || err.Error() != "请填写名称" {
		t.Errorf("Msg over builtin: got %v", err)
	}
	if err := Msg(Required, "replaced")("ok"); err != nil {
		t.Errorf("Msg must not turn a passing value into a failure: %v", err)
	}

	noVowels := func(value string) error {
		if strings.ContainsAny(value, "aeiou") {
			return errors.New("original")
		}
		return nil
	}
	if err := Msg(noVowels, "no vowels allowed")("hat"); err == nil || err.Error() != "no vowels allowed" {
		t.Errorf("Msg over a user rule: got %v", err)
	}
}

func TestFieldStopsAtFirstFailure(t *testing.T) {
	later := 0
	counting := func(string) error { later++; return errors.New("second") }

	v := New()
	v.Field("name", "", Required, counting)

	if got := v.Errors()["name"]; got != "can't be blank" {
		t.Errorf("want the first rule's message, got %q", got)
	}
	// The point of this assertion: a DB-backed rule placed after Required is
	// never invoked for blank input, so no query is issued.
	if later != 0 {
		t.Errorf("rules after a failure must not run, ran %d", later)
	}
}

func TestSecondFieldCallCannotOverwrite(t *testing.T) {
	ran := 0
	counting := func(string) error { ran++; return nil }

	v := New()
	v.Field("name", "", Required)
	v.Field("name", "anything", counting)

	if got := v.Errors()["name"]; got != "can't be blank" {
		t.Errorf("want the original message, got %q", got)
	}
	if ran != 0 {
		t.Errorf("an already-failed field must run no rules, ran %d", ran)
	}
}

func TestFieldRecordsOnlyFailures(t *testing.T) {
	v := New()
	v.Field("name", "Amy", Required, MaxLen(80))
	v.Field("email", "amy@example.com", Required, Email)

	if !v.OK() {
		t.Fatalf("want OK, got errors %v", v.Errors())
	}
	if v.Errors() != nil {
		t.Errorf("Errors() must stay nil until a failure, got %v", v.Errors())
	}
}

func TestFieldWithoutRules(t *testing.T) {
	v := New()
	v.Field("name", "")
	if !v.OK() {
		t.Errorf("a field with no rules cannot fail, got %v", v.Errors())
	}
}

func TestCheck(t *testing.T) {
	t.Run("records when false", func(t *testing.T) {
		v := New()
		v.Check(false, "confirm", "doesn't match")
		if got := v.Errors()["confirm"]; got != "doesn't match" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("no-op when true", func(t *testing.T) {
		v := New()
		v.Check(true, "confirm", "doesn't match")
		if !v.OK() {
			t.Errorf("want OK, got %v", v.Errors())
		}
	})

	t.Run("no-op on an already failed field", func(t *testing.T) {
		v := New()
		v.Field("name", "", Required)
		v.Check(false, "name", "second message")
		if got := v.Errors()["name"]; got != "can't be blank" {
			t.Errorf("want the original message, got %q", got)
		}
	})

	t.Run("reports on a field Field never saw", func(t *testing.T) {
		v := New()
		v.Check(false, "untouched", "bad")
		if _, ok := v.Errors()["untouched"]; !ok {
			t.Error("want an entry for a field never passed to Field")
		}
	})
}

func TestFreshValidator(t *testing.T) {
	v := New()
	if !v.OK() {
		t.Error("a fresh validator must be OK")
	}
	if v.Errors() != nil {
		t.Errorf("a fresh validator must have nil Errors(), got %v", v.Errors())
	}
}

func TestChaining(t *testing.T) {
	v := New()
	got := v.Field("a", "x", Required).Check(true, "b", "no").Field("c", "", Required)

	if got != v {
		t.Error("Field and Check must return the same validator for chaining")
	}
	if len(v.Errors()) != 1 {
		t.Errorf("want exactly one error, got %v", v.Errors())
	}
}
