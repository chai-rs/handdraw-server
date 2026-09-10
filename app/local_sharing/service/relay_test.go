package service_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/app/local_sharing/model"
	"github.com/chai-rs/handdraw-server/app/local_sharing/service"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/stretchr/testify/require"
)

func user(t *testing.T) string {
	t.Helper()
	id, err := resourceid.New(model.UserIDPrefix)
	require.NoError(t, err)
	return id
}

func host(t *testing.T, r *service.Relay) (string, model.Admission, model.Subscription) {
	t.Helper()
	actor := user(t)
	a, err := r.Create(actor)
	require.NoError(t, err)
	p, err := r.Attach(a.ID, a.Token)
	require.NoError(t, err)
	return actor, a, p
}

func viewer(t *testing.T, r *service.Relay, a model.Admission) (string, model.Subscription) {
	t.Helper()
	actor := user(t)
	g, err := r.Join(a.ID, actor, a.InviteToken)
	require.NoError(t, err)
	p, err := r.Attach(a.ID, g.Token)
	require.NoError(t, err)
	return actor, p
}

func TestViewerCannotPublishOrBecomeHostAndLateJoinSeesLatestSource(t *testing.T) {
	r := service.New(nil)
	defer r.Close()
	owner, a, h := host(t, r)
	raw := []byte(`{"schema_version":1,"name":"Allocation"}`)
	require.NoError(t, r.Publish(h.ID, raw))
	raw[0] = 'x'
	actor, v := viewer(t, r, a)
	event := <-v.Events
	require.JSONEq(t, `{"schema_version":1,"name":"Allocation"}`, string(event.Snapshot))
	require.Equal(t, "viewer", event.Role)
	require.ErrorIs(t, r.Publish(v.ID, []byte(`{"changed":true}`)), model.ErrDenied)
	require.ErrorIs(t, r.Revoke(a.ID, actor), model.ErrDenied)
	hostToken, err := r.Renew(a.ID, owner)
	require.NoError(t, err)
	require.ErrorIs(t, r.Reauthenticate(v.ID, hostToken.Token), model.ErrDenied)
	require.NoError(t, r.Publish(h.ID, []byte(`{"new":true}`)))
	_, late := viewer(t, r, a)
	require.JSONEq(t, `{"new":true}`, string((<-late.Events).Snapshot))
	r.Detach(h.ID)
	require.Equal(t, "host_disconnected", <-v.Ended)
	require.Zero(t, r.Stats())
	_, err = r.Join(a.ID, actor, a.InviteToken)
	require.ErrorIs(t, err, model.ErrEnded)
}

func TestActiveSessionSurvivesTwoHoursButRenewalCannotExtendIdle(t *testing.T) {
	now := time.Now()
	r := service.New(func() time.Time { return now })
	defer r.Close()
	actor, a, h := host(t, r)
	for range 40 {
		now = now.Add(4 * time.Minute)
		g, err := r.Renew(a.ID, actor)
		require.NoError(t, err)
		require.NoError(t, r.Reauthenticate(h.ID, g.Token))
		require.NoError(t, r.Activity(h.ID, "pan_zoom"))
	}
	require.Equal(t, 1, r.Stats().Sessions)
	for range 7 {
		now = now.Add(4 * time.Minute)
		g, err := r.Renew(a.ID, actor)
		require.NoError(t, err)
		require.NoError(t, r.Reauthenticate(h.ID, g.Token))
	}
	now = now.Add(2 * time.Minute)
	require.ErrorIs(t, r.Activity(h.ID, "keep_alive"), model.ErrEnded)
	require.Equal(t, "idle_timeout", <-h.Ended)
	require.Zero(t, r.Stats())
}

func TestExpiryAndRestartRejectStaleAdmissionWithoutRetainingContent(t *testing.T) {
	for _, reason := range []string{"host_absent", "token_expired", "relay_stopped"} {
		t.Run(reason, func(t *testing.T) {
			now := time.Now()
			r := service.New(func() time.Time { return now })
			defer r.Close()
			actor := user(t)
			a, err := r.Create(actor)
			require.NoError(t, err)
			if reason == "host_absent" {
				now = now.Add(10 * time.Second)
				r.Sweep()
			} else {
				p, err := r.Attach(a.ID, a.Token)
				require.NoError(t, err)
				require.NoError(t, r.Publish(p.ID, []byte(`{"private":"source"}`)))
				if reason == "token_expired" {
					now = now.Add(model.TokenTTL)
					r.Sweep()
				} else {
					r.Close()
				}
				require.Equal(t, reason, <-p.Ended)
			}
			_, err = r.Attach(a.ID, a.Token)
			require.ErrorIs(t, err, model.ErrEnded)
			require.Zero(t, r.Stats())
		})
	}
}

func TestConcurrentViewerAdmissionCannotExceedFivePeers(t *testing.T) {
	r := service.New(nil)
	defer r.Close()
	_, a, _ := host(t, r)
	var admitted int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 12 {
		actor := user(t)
		wg.Go(func() {
			g, err := r.Join(a.ID, actor, a.InviteToken)
			if err != nil {
				return
			}
			_, err = r.Attach(a.ID, g.Token)
			if err == nil {
				mu.Lock()
				admitted++
				mu.Unlock()
			} else {
				require.ErrorIs(t, err, model.ErrBudget)
			}
		})
	}
	wg.Wait()
	require.Equal(t, 4, admitted)
	require.Equal(t, model.MaxPeers, r.Stats().Connections)
}

func TestSnapshotsRespectPerRoomAndGlobalMemoryBudgets(t *testing.T) {
	r := service.New(nil)
	defer r.Close()
	payload := make([]byte, model.MaxSnapshotBytes)
	for i := range payload {
		payload[i] = ' '
	}
	payload[0] = '['
	payload[len(payload)-1] = ']'
	require.True(t, json.Valid(payload))
	for range 4 {
		_, _, h := host(t, r)
		require.NoError(t, r.Publish(h.ID, payload))
	}
	_, _, h := host(t, r)
	require.ErrorIs(t, r.Publish(h.ID, payload), model.ErrBudget)
	require.Equal(t, model.MaxStoredBytes, r.Stats().StoredBytes)
	require.ErrorIs(t, r.Publish(h.ID, append(payload, ' ')), model.ErrInvalid)
	r.Close()
	require.Zero(t, r.Stats())
}
