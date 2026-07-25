// Package validate turns submitted form values into a per-field error map, so a
// handler can re-render the form with a message under each bad input.
//
// A rule is a value, not a method on the validator: anything with the signature
// func(string) error is a rule. That is what keeps this package stdlib-only —
// checks that need the database (uniqueness) are ordinary closures written in
// the handler, where the query and its error belong.
//
// Errors never cross a redirect. Success messages do, and that is what the
// session's flash is for; see internal/service/session.
package validate

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Rule checks one submitted value. A nil error means the value passed; a
// non-nil error's message reaches the user verbatim, so it should read as a
// predicate about the field ("can't be blank") rather than a sentence.
//
// Every rule here except Required treats a blank value as passing, which is what
// makes an optional field a bare rule list — blank is accepted, non-blank must
// hold up. A rule of your own should follow that convention; nothing enforces it.
type Rule func(value string) error

// Validator collects at most one error message per field for one submission.
// Not safe for concurrent use; make one per request.
type Validator struct {
	errs map[string]string // field → message; allocated on the first failure
}

// New returns an empty validator.
func New() *Validator { return &Validator{} }

// Field runs rules against value in order and stops at the first failure,
// recording its message under name. If name already has an error, no rule runs
// at all — so a second Field call cannot overwrite the first message, and an
// expensive rule is skipped once a cheap one ahead of it has failed.
func (v *Validator) Field(name, value string, rules ...Rule) *Validator {
	if _, failed := v.errs[name]; failed {
		return v
	}
	for _, rule := range rules {
		if err := rule(value); err != nil {
			v.fail(name, err.Error())
			return v
		}
	}
	return v
}

// Check records msg for field when ok is false. It covers assertions that span
// two fields (password confirmation) and, unlike Field, can report on a field
// that was never passed to Field.
func (v *Validator) Check(ok bool, field, msg string) *Validator {
	if ok {
		return v
	}
	if _, failed := v.errs[field]; failed {
		return v
	}
	v.fail(field, msg)
	return v
}

// OK reports whether every field passed.
func (v *Validator) OK() bool { return len(v.errs) == 0 }

// Errors returns the live field→message map for the `errors` page prop. It is
// nil until something fails, so set the prop only when OK reports false.
func (v *Validator) Errors() map[string]string { return v.errs }

func (v *Validator) fail(field, msg string) {
	if v.errs == nil {
		v.errs = make(map[string]string, 4)
	}
	v.errs[field] = msg
}

// blank is the emptiness test every rule shares: content, ignoring surrounding
// whitespace.
func blank(value string) bool { return strings.TrimSpace(value) == "" }

// optional wraps a rule so a blank value passes it, leaving presence to Required.
func optional(r Rule) Rule {
	return func(value string) error {
		if blank(value) {
			return nil
		}
		return r(value)
	}
}

// Required rejects a value that is empty or only whitespace. It is the one rule
// a blank value does not slip past.
var Required Rule = func(value string) error {
	if blank(value) {
		return errors.New("can't be blank")
	}
	return nil
}

// Email accepts what net/mail reads as a single address.
var Email Rule = optional(func(value string) error {
	if _, err := mail.ParseAddress(value); err != nil {
		return errors.New("must be a valid email")
	}
	return nil
})

// Int accepts a base-10 integer.
var Int Rule = optional(func(value string) error {
	if _, err := strconv.ParseInt(value, 10, 64); err != nil {
		return errors.New("must be a number")
	}
	return nil
})

// MinLen requires at least n runes — runes, so a multi-byte value is not
// measured in bytes.
func MinLen(n int) Rule {
	return optional(func(value string) error {
		if utf8.RuneCountInString(value) < n {
			return fmt.Errorf("must be at least %d characters", n)
		}
		return nil
	})
}

// MaxLen requires at most n runes.
func MaxLen(n int) Rule {
	return optional(func(value string) error {
		if utf8.RuneCountInString(value) > n {
			return fmt.Errorf("must be at most %d characters", n)
		}
		return nil
	})
}

// Match requires the value to match re. Its message says only that the value is
// invalid, because a regexp has no readable form — wrap it in Msg to say why.
func Match(re *regexp.Regexp) Rule {
	return optional(func(value string) error {
		if !re.MatchString(value) {
			return errors.New("is not valid")
		}
		return nil
	})
}

// In requires the value to be one of allowed.
func In(allowed ...string) Rule {
	return optional(func(value string) error {
		if slices.Contains(allowed, value) {
			return nil
		}
		return fmt.Errorf("must be one of: %s", strings.Join(allowed, ", "))
	})
}

// Msg replaces the message r would produce. It is the only way to customize
// text, and it works on a rule of your own as well as the ones here.
func Msg(r Rule, text string) Rule {
	return func(value string) error {
		if r(value) != nil {
			return errors.New(text)
		}
		return nil
	}
}
