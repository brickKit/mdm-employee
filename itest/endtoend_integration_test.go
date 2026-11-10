//go:build integration

// Package itest runs mdm/employee against PostgreSQL and the bus, through its real surfaces (the user surface with
// be-sdk-go sectest's tokens, the system surface over mTLS, events). mdm/org is a stand-in that speaks its published
// contract (its source methods) and whose events the test publishes on mdm.org itself.
package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	employee "github.com/brickKit/mdm-employee/v3/employee"

	core "github.com/brickKit/mdm-employee/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/component"
	"github.com/brickKit/be-sdk-go/v3/config"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/events/bus"
	"github.com/brickKit/be-sdk-go/v3/events/jetstream"
	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/be-sdk-go/v3/runtime"
	"github.com/brickKit/be-sdk-go/v3/sectest"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	itPort   = "28227"
	itBase   = "http://localhost:" + itPort
	itSystem = "localhost:9227"
	itOrg    = "localhost:19301"
	itSchema = "mdm_employee_it"
)

// fixture is the running member, the stand-ins and the world's IDs the steps share.
type fixture struct {
	t     *testing.T
	ctx   context.Context
	w     *sectest.World
	admin *pgx.Conn
	rt    *runtime.Runtime
	tr    events.Transport
	org   *orgStandIn
	alice client
	// mdm/org's world: a legal entity in Madrid with two units and their positions, created by start
	le, sales, ops, rep, clerk, head string
	// employees the steps pass on
	ana, eva, gil, ivo, ivoLink string
	kai, kaiAppt                string // E6: a termination and a last day registered for later
}

func TestEmployeeEndToEnd(t *testing.T) {
	fx := start(t)
	fx.orgProjection()
	fx.employees()
	fx.appointments()
	fx.links()
	fx.evidence()
	fx.replay()
	fx.source()
	fx.recoveryChecks()
}

// start makes the world, the mdm/org stand-in and its stream, then migrates and runs the member.
func start(t *testing.T) *fixture {
	adminURL, natsURL := os.Getenv("BE_SDK_TEST_PG_ADMIN"), os.Getenv("BE_SDK_TEST_NATS_URL")
	if adminURL == "" || natsURL == "" {
		t.Fatal("BE_SDK_TEST_PG_ADMIN and BE_SDK_TEST_NATS_URL are needed")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	require.NoError(t, err)
	t.Cleanup(func() { admin.Close(ctx) })
	_, err = admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+itSchema+" CASCADE; CREATE SCHEMA "+itSchema)
	require.NoError(t, err)
	u, err := url.Parse(adminURL)
	require.NoError(t, err)
	pw, _ := u.User.Password()
	w := sectest.New(t)
	w.GrantAll(sectest.Alice, core.Definitions())

	fx := &fixture{t: t, ctx: ctx, w: w, admin: admin, le: ids.New().String(), sales: ids.New().String(),
		ops: ids.New().String(), rep: ids.New().String(), clerk: ids.New().String(), head: ids.New().String()}
	fx.org = newOrgStandIn()
	fx.org.put(orgv3.Aggregate_AGGREGATE_LEGAL_ENTITY, map[string]any{"id": fx.le, "code": "ES01", "state": "active",
		"timezone": "Europe/Madrid", "version": int64(1)})
	fx.org.put(orgv3.Aggregate_AGGREGATE_ORG_UNIT, fx.unit(fx.sales, fx.le+"/"+fx.sales, "active", 1))
	fx.org.put(orgv3.Aggregate_AGGREGATE_ORG_UNIT, fx.unit(fx.ops, fx.le+"/"+fx.ops, "active", 1))
	fx.org.put(orgv3.Aggregate_AGGREGATE_POSITION, fx.position(fx.rep, fx.sales, "SALES-REP", "active", 1))
	fx.org.put(orgv3.Aggregate_AGGREGATE_POSITION, fx.position(fx.head, fx.sales, "SALES-MGR", "active", 1))
	fx.org.put(orgv3.Aggregate_AGGREGATE_POSITION, fx.position(fx.clerk, fx.ops, "OPS-CLERK", "active", 1))
	portFree(t, itPort, itSystem, itOrg)
	serve(t, itOrg, w, core.Org, func(s *grpc.Server) { orgv3.RegisterSourceServiceServer(s, fx.org) })

	// the shared NATS keeps what earlier runs and cells left on these streams (other implementation instances, a newer
	// epoch): the projection would judge them and skip this run's events. A clean stream each run.
	js, err := jetstream.Connect(natsURL, "employee-it-clean")
	require.NoError(t, err)
	for _, s := range []string{orgStream.Name, core.Stream} {
		_ = js.DeleteStream(ctx, s) // not there: nothing to clean
	}
	_ = js.Close()
	// mdm/org's stream exists before the subscription starts (mdm/org's own forwarder makes it in a deployment)
	fx.tr, err = bus.Open(ctx, natsURL, "employee-it-org", slog.Default())
	require.NoError(t, err)
	t.Cleanup(func() { fx.tr.Close() })
	require.NoError(t, fx.tr.EnsureStream(ctx, orgStream))

	env := w.Env(t, core.ID)
	for k, v := range map[string]string{"COMPONENT_ID": core.ID, "COMPONENT_VERSION": employee.Version, "PORT": itPort,
		"STOP_DRAIN_DELAY": "50ms", "PG_HOST": u.Hostname(), "PG_PORT": u.Port(), "PG_DATABASE": strings.TrimPrefix(u.Path, "/"),
		"PG_USER": u.User.Username(), "PG_PASSWORD": pw, "PG_SCHEMA": itSchema, "PG_OWNER_USER": u.User.Username(),
		"PG_OWNER_PASSWORD": pw, "BUS_URL": natsURL, "IMPLEMENTATION_INSTANCE": ids.New().String(),
		"MDM_ORG_GRPC_ENDPOINT": "http://" + itOrg} {
		env[k] = v
	}
	var logs bytes.Buffer
	var logMu sync.Mutex
	t.Cleanup(func() {
		if t.Failed() {
			logMu.Lock()
			t.Log(logs.String())
			logMu.Unlock()
		}
	})
	out := &lockedWriter{w: &logs, mu: &logMu}
	opts := launcher.Options{Stdout: out, Stderr: out,
		Env: config.Env{LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok }, Read: os.ReadFile}}
	migrate := opts
	migrate.Args = []string{"migrate"}
	def := employee.Definition()
	newModule := def.New
	def.New = func(rt *runtime.Runtime) (component.Module, error) { fx.rt = rt; return newModule(rt) }
	require.Equal(t, launcher.ExitOK, launcher.Run(ctx, def, migrate))
	sctx, stop := context.WithCancel(ctx)
	done := make(chan int, 1)
	go func() { done <- launcher.Run(sctx, def, opts) }()
	t.Cleanup(func() { stop(); <-done })
	require.Eventually(t, func() bool {
		r, err := http.Get(itBase + "/readyz")
		if err == nil {
			r.Body.Close()
		}
		return err == nil && r.StatusCode == 200
	}, 20*time.Second, 100*time.Millisecond)
	fx.alice = client{t, w.Bearer(sectest.Alice)}
	return fx
}

var orgStream = events.Stream{Name: core.OrgStream, Partitions: 4}

func (fx *fixture) unit(id, path, state string, version int64) map[string]any {
	return map[string]any{"id": id, "legal_entity_id": fx.le, "path": path, "state": state, "version": version}
}

func (fx *fixture) position(id, unit, code, state string, version int64) map[string]any {
	return map[string]any{"id": id, "legal_entity_id": fx.le, "unit_id": unit, "code": code, "name": code,
		"state": state, "version": version}
}

// publishOrg publishes an event on mdm/org's stream as its forwarder would (any type: a later mdm/org's too).
func (fx *fixture) publishOrg(typ, id string, version int64, kind events.Projection, data []byte) {
	row := events.Row{EventID: ids.New().String(), Stream: orgStream.Name, Partition: orgStream.PartitionOf(id),
		Type: typ, OccurredAt: time.Now(), RecordID: id, Version: version,
		Projection: kind, Payload: data, Seq: time.Now().UnixNano(), Epoch: fx.org.epoch(fx.t)}
	row.Digest = events.Digest(row.EventID, data)
	require.NoError(fx.t, fx.tr.Publish(fx.ctx, events.Message{Subject: events.Subject(orgStream.Name, row.Partition, row.Type),
		Headers: row.Headers(core.Org, orgStamp.GetInstance()), Data: data}))
}

// change updates a record of the stand-in and publishes its full-state event.
func (fx *fixture) change(a orgv3.Aggregate, typ string, rec map[string]any) {
	fx.org.put(a, rec)
	data, _ := json.Marshal(rec)
	fx.publishOrg(typ, rec["id"].(string), rec["version"].(int64), events.Full, data)
}

// projected reads one projection row: its state and data (empty when there is none).
func (fx *fixture) projected(projection, id string) (string, map[string]any) {
	var state string
	var raw []byte
	err := fx.admin.QueryRow(fx.ctx, "SELECT state, data FROM "+itSchema+".be_projection WHERE projection = $1 AND record_id = $2",
		projection, id).Scan(&state, &raw)
	if err != nil {
		return "", nil
	}
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	return state, data
}

// due makes a singleton job due now (its work item's ID is the job's name).
func (fx *fixture) due(job string) {
	fx.t.Helper()
	require.Eventually(fx.t, func() bool {
		tag, err := fx.admin.Exec(fx.ctx, "UPDATE "+itSchema+".be_work_item SET run_at = now() WHERE id = $1 AND state = 'pending'", job)
		return err == nil && tag.RowsAffected() == 1
	}, 10*time.Second, 100*time.Millisecond, "the singleton %s made due now", job)
}

// at reads a value along a path of keys and indexes from a decoded JSON object.
func at(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			a, _ := v.([]any)
			if k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

type client struct {
	t     *testing.T
	token string
}

// do calls the user surface; a POST carries a new Idempotency-Key.
func (c client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, itBase+path, r)
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "itest")
	if method == "POST" {
		req.Header.Set("Idempotency-Key", ids.New().String())
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func serve(t *testing.T, addr string, w *sectest.World, as string, register func(*grpc.Server)) {
	ln, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(w.TLS(t, as).ServerConfig())))
	register(srv)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// portFree fails at once when a port the member needs is taken: a running cell or another member would answer in
// its place, and the test would assert against another process.
func portFree(t *testing.T, addrs ...string) {
	for _, a := range addrs {
		if !strings.Contains(a, ":") {
			a = "localhost:" + a
		}
		ln, err := net.Listen("tcp", a)
		require.NoError(t, err, fmt.Sprintf("port %s is taken: is a cell or another member running?", a))
		_ = ln.Close()
	}
}
