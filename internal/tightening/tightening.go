// Package tightening is the evidence this member records (assembly protocol 15's evidence table; be-protocol
// P12.15-P12.18): a termination, the end of an appointment and the end of an account link narrow what someone may do
// (the authorization member revokes what was bound to them), so each writes its intent before the change, outside
// any transaction, and its conclusion in the change's transaction; one that does not take place is cancelled. Hires,
// appointments and links narrow nothing and are not recorded; a termination ends its appointments under its own
// record (the replay of it ends them again).
package tightening

import (
	"context"
	"strconv"

	core "github.com/brickKit/mdm-employee/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/evidence"
)

// Category is the evidence category of every record here; the change's "kind" tells termination, appointment_end and
// link_end apart.
const Category = "employee_tightening"

// The object types of this member's evidence.
const (
	EmployeeType    = "mdm.employee.employee"
	AppointmentType = "mdm.employee.appointment"
	LinkType        = "mdm.employee.account_link"
)

// Tightening describes a change that may narrow access, for a record of type T.
type Tightening[T any] struct {
	Type string
	// Due reports, from the record read without a lock before the change, whether the change may tighten: the intent
	// is written then. It must hold whenever the change under the lock can tighten (the intent comes first).
	Due func(before T) bool
	// Change is the change itself (what it does, not the resulting state), from the record read before.
	Change func(before T) map[string]any
	// Tightens reports whether the change made under the lock narrowed anything: only then is it concluded.
	Tightens func(before, after T) bool
}

// Intent writes a tightening's intent; nil when the member writes no evidence or the change cannot tighten.
func Intent[T any](ctx context.Context, w *evidence.Writer, t *Tightening[T], id string, version int64, before T) *evidence.Pending {
	if w == nil || t == nil || !t.Due(before) {
		return nil
	}
	p, err := w.Intent(ctx, Category, "", evidence.Object{Type: t.Type, ID: id, Version: strconv.FormatInt(version, 10)},
		t.Change(before))
	if err != nil { // the record could not be encoded: a defect here, never a reason to hold the change back
		if w.Log != nil {
			w.Log.Error("an intent could not be sealed", "event", "evidence_intent_unsealed", "object", id, "error", err)
		}
		return nil
	}
	return p
}

// Conclude writes the effective conclusion in the change's transaction when the change tightened.
func Conclude[T any](ctx context.Context, tx db.Tx, p *evidence.Pending, t *Tightening[T], before, after T) (bool, error) {
	if p == nil || !t.Tightens(before, after) {
		return false, nil
	}
	return true, p.Effective(ctx, tx)
}

// Settle writes cancelled when the change whose intent p is did not tighten (refused, no change, rolled back).
func Settle(ctx context.Context, p *evidence.Pending, concluded bool) {
	if p != nil && !concluded {
		_ = p.Cancelled(context.WithoutCancel(ctx))
	}
}

// Termination: an active employee terminated as of a date (today or before: it takes effect now).
func Termination(date, today string) *Tightening[core.Employee] {
	return &Tightening[core.Employee]{Type: EmployeeType,
		Due: func(e core.Employee) bool { return e.State == "active" && date <= today },
		Change: func(core.Employee) map[string]any {
			return map[string]any{"kind": "termination", "termination_date": date}
		},
		Tightens: func(b, a core.Employee) bool { return b.State == "active" && a.State == "terminated" },
	}
}

// AppointmentEnd: an open appointment ended now, for a reason, its last day given.
func AppointmentEnd(reason, last string) *Tightening[core.Appointment] {
	return &Tightening[core.Appointment]{Type: AppointmentType,
		Due: func(a core.Appointment) bool { return a.State != "ended" },
		Change: func(core.Appointment) map[string]any {
			return map[string]any{"kind": "appointment_end", "reason": reason, "end_date": last}
		},
		Tightens: func(b, a core.Appointment) bool { return b.State != "ended" && a.State == "ended" },
	}
}

// LinkEnd: an active account link ended with a confirmation.
func LinkEnd(confirmation string) *Tightening[core.Link] {
	return &Tightening[core.Link]{Type: LinkType,
		Due: func(l core.Link) bool { return l.State == "active" },
		Change: func(l core.Link) map[string]any {
			return map[string]any{"kind": "link_end", "account": l.Account, "end_confirmation": confirmation}
		},
		Tightens: func(b, a core.Link) bool { return b.State == "active" && a.State == "ended" },
	}
}

// ScheduledTermination: an active employee's termination registered for a later date. Nothing ends yet, but what
// comes is narrowed: a full recovery to before the registration must not forget it, so it is recorded when registered;
// a replay registers the date again and the effective-dates job carries it out when it comes (with its own record).
// An earlier date than one registered narrows too; a later one or a withdrawal widens and writes none.
func ScheduledTermination(date, today string) *Tightening[core.Employee] {
	return &Tightening[core.Employee]{Type: EmployeeType,
		Due: func(e core.Employee) bool {
			return e.State == "active" && date > today && (e.TerminationDate == nil || *e.TerminationDate > date)
		},
		Change: func(core.Employee) map[string]any {
			return map[string]any{"kind": "termination_scheduled", "termination_date": date}
		},
		Tightens: func(b, a core.Employee) bool {
			return a.State == "active" && earlier(a.TerminationDate, b.TerminationDate)
		},
	}
}

// ScheduledEnd: an open appointment's last day set to a later date (recorded as ScheduledTermination is).
func ScheduledEnd(last string) *Tightening[core.Appointment] {
	return &Tightening[core.Appointment]{Type: AppointmentType,
		Due: func(a core.Appointment) bool { return a.State != "ended" && (a.EndDate == nil || *a.EndDate > last) },
		Change: func(core.Appointment) map[string]any {
			return map[string]any{"kind": "appointment_end_scheduled", "end_date": last}
		},
		Tightens: func(b, a core.Appointment) bool { return a.State != "ended" && earlier(a.EndDate, b.EndDate) },
	}
}

// earlier: a date set after where there was none or a later one.
func earlier(after, before *string) bool {
	return after != nil && (before == nil || *after < *before)
}
