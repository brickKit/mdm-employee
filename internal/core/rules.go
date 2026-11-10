package core

import (
	"context"
	"regexp"
	"strings"

	"github.com/brickKit/be-sdk-go/v3/audit"
	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/errs"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/identity"
	"github.com/brickKit/be-sdk-go/v3/list"
	"github.com/brickKit/be-sdk-go/v3/search"
)

// The component's own reasons (the SDK's catalog has the shared ones).
var (
	ErrVersion = errs.Define(ID, "VERSION_CONFLICT", errs.Aborted, 409, errs.RetryNo, "The record changed since that version")
	ErrNumber  = errs.Define(ID, "EMPLOYEE_NO_TAKEN", errs.AlreadyExists, 409, errs.RetryNo,
		"The employee number belongs to another employee of the legal entity")
	ErrState = errs.Define(ID, "STATE_CONFLICT", errs.FailedPrecondition, 409, errs.RetryNo,
		"The record's state does not allow this")
	ErrOverlap = errs.Define(ID, "APPOINTMENT_OVERLAP", errs.FailedPrecondition, 409, errs.RetryNo,
		"The employee already holds the position for part of that period")
	ErrOrgUnknown = errs.Define(ID, "ORG_NOT_KNOWN", errs.FailedPrecondition, 409, errs.RetryYes,
		"mdm/org's record is not known here yet, or not active")
	ErrLinked = errs.Define(ID, "ACCOUNT_LINKED", errs.AlreadyExists, 409, errs.RetryNo,
		"The account is linked to an employee already")
	ErrConfirmationUsed = errs.Define(ID, "CONFIRMATION_USED", errs.FailedPrecondition, 409, errs.RetryNo,
		"The confirmation was used for another change")
)

var (
	NumberForm = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,31}$`)
	UUIDForm   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	DateForm   = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// Unsupported answers a route of the contract this version does not have (D75).
func Unsupported(c *httpx.Context) error {
	if _, _, err := Check(c, c.Operation()); err != nil {
		return err
	}
	return errs.New(errs.ReasonCapabilityUnsupported, "not in this version of mdm/employee (the payee role, imports "+
		"and self-service come later)")
}

// Check admits the request's subject to op and returns the context to work in and the acting principal.
func Check(c *httpx.Context, op string) (context.Context, string, error) {
	s, _ := sdkauthz.From(c.Ctx())
	ctx, _, err := s.Check(c.Ctx(), op, sdkauthz.Facts{})
	if err != nil {
		return ctx, "", err
	}
	u, _ := identity.UserFrom(ctx)
	return ctx, u.Subject, nil
}

func Invalid(field, why string) error {
	return errs.New(errs.ReasonRequestInvalid, why).WithViolations(errs.Violation{Field: field, Reason: "INVALID", Description: why})
}

func NotFound(what string) error { return errs.New(errs.ReasonNotFound, what+" not found") }

// Change records the business change with the record's new state (protocol 03), in the transaction that made it.
func Change(m *Base, ctx context.Context, tx db.Tx, actor, op, typ, id string, after any) error {
	_, err := m.RT.Audit.Write(ctx, tx, audit.Record{Category: audit.BusinessChange, Operation: op, Outcome: audit.Succeeded,
		Actor: actor, Target: audit.TargetOf(typ, id, after), Changes: map[string]any{"after": after}})
	return err
}

// CheckNames refuses translations without a language tag or a name.
func CheckNames(names map[string]string) error {
	for tag, n := range names {
		if tag == "" || len(tag) > 35 || strings.TrimSpace(n) == "" {
			return Invalid("names", "names maps a language tag to a non-empty name")
		}
	}
	return nil
}

func SearchOf(no, name string, names map[string]string) string {
	vals := []string{no, name}
	for _, n := range names {
		vals = append(vals, n)
	}
	return search.Text(vals...)
}

// Build compiles a list request for the caller (security 07): the spec's filters, keyword and sort, the cursor.
func Build(c *httpx.Context, spec list.Spec, req list.Request) (context.Context, *list.Query, error) {
	limit, err := list.ParseLimit(c.Query("limit"))
	if err != nil {
		return nil, nil, err
	}
	req.Limit, req.Cursor = limit, c.Query("cursor")
	s, _ := sdkauthz.From(c.Ctx())
	return list.Build(c.Ctx(), s, spec, req)
}

func Eq(c *httpx.Context, fields ...string) []list.Filter {
	var out []list.Filter
	for _, f := range fields {
		if v := c.Query(f); v != "" {
			out = append(out, list.Filter{Field: f, Op: "eq", Values: []string{v}})
		}
	}
	return out
}

// Principal is the acting user's principal in an admitted request's context.
func Principal(ctx context.Context) string {
	u, _ := identity.UserFrom(ctx)
	return u.Subject
}

// ResourceOf is one of the component's resource definitions (for masking an output, P8.31).
func ResourceOf(typ string) *sdkauthz.Resource {
	for _, r := range Definitions().Resources {
		if r.Type == typ {
			return &r
		}
	}
	return nil
}
