package employee

import (
	"context"

	core "github.com/brickKit/mdm-employee/v3/internal/core"
	replay "github.com/brickKit/mdm-employee/v3/internal/replay"
	tightening "github.com/brickKit/mdm-employee/v3/internal/tightening"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/evidence"
	"github.com/brickKit/be-sdk-go/v3/recovery"
)

// RecoveryChecks registers what a full recovery asks this member (be-protocol chapter 14): whether the objects this
// member audits are still here, at which version.
func (m *Module) RecoveryChecks(c *recovery.Checks) error {
	c.Presence(core.Employees, recovery.TablePresence("employee", "id", "version"))
	c.Presence(core.Appointments, recovery.TablePresence("appointment", "id", "version"))
	c.Presence(core.Links, recovery.TablePresence("account_link", "id", "version"))
	return nil
}

// Replays registers the replay of employee_tightening after a full recovery (evidence.Replayer).
func (m *Module) Replays(r *evidence.Replays) error {
	r.Add(tightening.Category, func(ctx context.Context, tx db.Tx, rec evidence.Replayed) error {
		return replay.Replay(m.b, ctx, tx, rec)
	})
	return nil
}
