// Package employee is mdm/employee (boundary card mdm/employee): employees, their appointments on a legal entity, a
// unit and a position for a period of business dates (qualification instances that are never reused, security 04),
// and the links of accounts to employees (a protected operation, security 08). Kept by people over the user surface;
// the stream mdm.employee is a watermark source with the record read (protocol 02), pulled by the authorization
// member. The payee role, imports and self-service answer CAPABILITY_UNSUPPORTED (D75).
package employee

import (
	"embed"
	"io/fs"

	core "github.com/brickKit/mdm-employee/v3/internal/core"
	source "github.com/brickKit/mdm-employee/v3/internal/source"

	"github.com/brickKit/be-sdk-go/v3/component"
	"github.com/brickKit/be-sdk-go/v3/config"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/runtime"
)

// Identity of the component.
const (
	Version  = "3.0.2"
	GRPCPort = 9227
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Keys are the component's own configuration keys: none.
func Keys() []config.Key { return nil }

// Own describes the own keys for the manifest's configSchema.
var Own = map[string]string{}

// Definition is the component as the launcher and the shells take it.
func Definition() component.Definition {
	return component.Definition{ID: core.ID, Version: Version, Keys: Keys(), Migrations: migrations(), GRPCPort: GRPCPort, Subscribes: true,
		Streams: []events.Stream{{Name: core.Stream, Partitions: core.Partitions,
			Watermark: &events.Watermark{Operation: core.OpPull, Pullers: []string{core.Authz}},
			Records:   &events.Records{Operation: core.OpSystem, Readers: []string{core.Authz}, Read: source.Read, Page: source.Page}}},
		Authz: core.Definitions(), Resend: core.OpResend, Recovery: core.OpRecover, Replay: core.OpReplay, Evidence: true,
		New: func(rt *runtime.Runtime) (component.Module, error) { return New(rt) }}
}
