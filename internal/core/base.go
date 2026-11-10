package core

import (
	"github.com/brickKit/be-sdk-go/v3/evidence"
	"github.com/brickKit/be-sdk-go/v3/runtime"
)

// Base is what every module works with: the component's runtime (its database, events, audit, calls, logger) and the
// evidence writer of the changes that tighten access (nil: no evidence, the module run alone in tests).
type Base struct {
	RT *runtime.Runtime
	Ev *evidence.Writer
}
