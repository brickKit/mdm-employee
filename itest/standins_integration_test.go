//go:build integration

package itest

// The stand-ins: mdm/org speaking its published contract (the source methods of its three aggregates), whose epoch the
// test can advance as a full recovery would.

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"testing"

	"github.com/brickKit/be-sdk-go/v3/events"
	orgv3 "github.com/brickKit/mdm-org/contract/v3/gen/go/be/mdm/org/v3"
	"github.com/stretchr/testify/require"
)

type orgStandIn struct {
	orgv3.UnimplementedSourceServiceServer
	mu   sync.Mutex
	recs map[orgv3.Aggregate]map[string]map[string]any
}

var orgStamp = &orgv3.Stamp{Instance: "org-it", Epoch: "1.1"}

func newOrgStandIn() *orgStandIn {
	return &orgStandIn{recs: map[orgv3.Aggregate]map[string]map[string]any{}}
}

func (o *orgStandIn) put(a orgv3.Aggregate, rec map[string]any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.recs[a] == nil {
		o.recs[a] = map[string]map[string]any{}
	}
	o.recs[a][rec["id"].(string)] = rec
}

func (o *orgStandIn) epoch(t *testing.T) events.Epoch {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, err := events.ParseEpoch(orgStamp.GetEpoch())
	require.NoError(t, err)
	return e
}

func (o *orgStandIn) Read(_ context.Context, in *orgv3.ReadRequest) (*orgv3.ReadResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := &orgv3.ReadResponse{Stamp: orgStamp}
	for _, id := range in.GetIds() {
		r := &orgv3.ReadResult{Id: id, State: orgv3.ReadState_READ_STATE_PURGED}
		if v, ok := o.recs[in.GetAggregate()][id]; ok {
			data, _ := json.Marshal(v)
			r = &orgv3.ReadResult{Id: id, State: orgv3.ReadState_READ_STATE_EXISTS, Version: v["version"].(int64), Data: data}
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

func (o *orgStandIn) Snapshot(_ context.Context, in *orgv3.SnapshotRequest) (*orgv3.SnapshotResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := &orgv3.SnapshotResponse{Stamp: orgStamp}
	if in.GetCursor() != "" {
		return out, nil
	}
	var keys []string
	for id := range o.recs[in.GetAggregate()] {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		v := o.recs[in.GetAggregate()][id]
		data, _ := json.Marshal(v)
		out.Records = append(out.Records, &orgv3.Record{Id: id, Version: v["version"].(int64), Data: data})
	}
	return out, nil
}
