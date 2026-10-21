//go:build !js

package mygo

import (
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/egoist/mygo/internal/platform"
)

func TestNetwork(t *testing.T) {
	set := func(online, metered bool) {
		onMain(func() { fb.Online, fb.Metered = online, metered })
	}
	set(true, false)
	defer set(false, false)
	if s := Network.Status(); s != (NetworkStatus{Online: true}) {
		t.Errorf("Status = %+v, want online", s)
	}
	set(true, true)
	if s := Network.Status(); s != (NetworkStatus{Online: true, Metered: true}) {
		t.Errorf("Status = %+v, want online and metered", s)
	}

	var got []NetworkStatus
	mainOK := true
	off := Network.OnChanged(func(s NetworkStatus) {
		got = append(got, s)
		mainOK = mainOK && isMainThread()
	})
	defer off()
	deadline := time.Now().Add(time.Second)
	for !onMainValue(func() bool { return fb.WatchingNetwork }) {
		if time.Now().After(deadline) {
			t.Fatal("the backend was not asked to watch the network")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The status when watching started is the one changes are told from,
	// and a report that changes nothing calls no listener.
	onMain(func() {
		fb.EmitNetwork(platform.NetworkStatus{Online: true, Metered: true})
		fb.EmitNetwork(platform.NetworkStatus{Online: false})
		fb.EmitNetwork(platform.NetworkStatus{Online: false})
		fb.EmitNetwork(platform.NetworkStatus{Online: true})
	})
	want := []NetworkStatus{{}, {Online: true}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("changes = %+v, want %+v", got, want)
	}
	if !mainOK {
		t.Error("OnChanged did not run on the main thread")
	}

	off()
	onMain(func() { fb.EmitNetwork(platform.NetworkStatus{Metered: true}) })
	if len(got) != len(want) {
		t.Errorf("changes after off = %+v, want %+v", got, want)
	}
}

// TestNetworkStatusJSON checks the status's JSON, which pages get through
// an event.
func TestNetworkStatusJSON(t *testing.T) {
	b, err := json.Marshal(NetworkStatus{Online: true, Metered: true})
	if err != nil || string(b) != `{"online":true,"metered":true}` {
		t.Errorf("JSON = %s, %v", b, err)
	}
}
