package core

import (
	"context"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/evidence"
	"github.com/brickKit/be-sdk-go/v3/runtime"
)

// Base is what every module works with: the component's runtime (its database, events, audit, calls, logger), the
// evidence writer of the changes that tighten access (nil: no evidence, the module run alone in tests), and this
// member's view of mdm/org.
type Base struct {
	RT  *runtime.Runtime
	Ev  *evidence.Writer
	Org OrgView
	// Terminated ends what an employee's termination ends beyond the employee (the appointments), in the termination's
	// transaction; the entry package wires the appointments module in. nil: nothing more.
	Terminated func(ctx context.Context, tx db.Tx, employee, date string) error
	// AdminWarning answers whether an account linked to the employee holds the authorization admin role (D62), asked
	// outside any transaction before a termination; the entry package wires the links module in. nil: never.
	AdminWarning func(ctx context.Context, employee string) (bool, error)
}
