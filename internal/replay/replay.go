// Package replay is the replay of this member's evidence in a full recovery's step 6 (assembly protocol 15, 6.3;
// be-protocol P12.19-P12.21): a termination, the end of an appointment and the end of an account link, applied again
// as recorded, without the business preconditions (they held when the change was made); only the object must still be
// there (otherwise dangling, for review). Each writes its event and its audit record as the change did, the recovery
// tool as the actor.
package replay

import (
	"context"
	"fmt"

	appointments "github.com/brickKit/mdm-employee/v3/internal/appointments"
	core "github.com/brickKit/mdm-employee/v3/internal/core"
	employees "github.com/brickKit/mdm-employee/v3/internal/employees"
	links "github.com/brickKit/mdm-employee/v3/internal/links"
	tightening "github.com/brickKit/mdm-employee/v3/internal/tightening"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/evidence"
)

// actor is the audit records' actor of a replay.
const actor = "ops/recover"

func Replay(m *core.Base, ctx context.Context, tx db.Tx, rec evidence.Replayed) error {
	kind, _ := rec.Change["kind"].(string)
	str := func(k string) string { s, _ := rec.Change[k].(string); return s }
	switch {
	case rec.Object.Type == tightening.EmployeeType && kind == "termination":
		return employees.ReplayTermination(m, ctx, tx, actor, rec.Object.ID, str("termination_date"))
	case rec.Object.Type == tightening.AppointmentType && kind == "appointment_end":
		return appointments.ReplayEnd(m, ctx, tx, actor, rec.Object.ID, str("reason"), str("end_date"))
	case rec.Object.Type == tightening.LinkType && kind == "link_end":
		return links.ReplayEnd(m, ctx, tx, actor, rec.Object.ID, str("end_confirmation"))
	}
	return fmt.Errorf("%s of kind %q on %s is not one this version replays", tightening.Category, kind, rec.Object.Type)
}
