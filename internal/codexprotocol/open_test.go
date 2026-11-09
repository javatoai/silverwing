package codexprotocol

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeSystem struct {
	results                     []error
	opens, repairs, discoveries int
	discoverErr, repairErr      error
	onRepair                    func()
	uris                        []string
}

func (s *fakeSystem) activate(_ context.Context, uri string) error {
	s.opens++
	s.uris = append(s.uris, uri)
	if len(s.results) == 0 {
		return nil
	}
	err := s.results[0]
	s.results = s.results[1:]
	return err
}
func (s *fakeSystem) lock(context.Context) (func(), error) { return func() {}, nil }
func (s *fakeSystem) discover(context.Context) (desktopCommand, error) {
	s.discoveries++
	return desktopCommand{Executable: "desktop"}, s.discoverErr
}
func (s *fakeSystem) repair(context.Context, desktopCommand) error {
	s.repairs++
	if s.onRepair != nil {
		s.onRepair()
	}
	return s.repairErr
}

func TestOpenRepairsOnlyMissingAssociations(t *testing.T) {
	denied := errors.New("access denied")
	for _, tt := range []struct {
		name           string
		results        []error
		repairs, opens int
		fail           bool
	}{
		{"healthy packaged delegate", []error{nil}, 0, 1, false},
		{"missing", []error{errAssociationMissing, errAssociationMissing, nil}, 1, 3, false},
		{"another window repaired", []error{errAssociationMissing, nil}, 0, 2, false},
		{"permission denied", []error{denied}, 0, 1, true},
		{"retry fails", []error{errAssociationMissing, errAssociationMissing, errAssociationMissing}, 1, 3, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeSystem{results: tt.results}
			uri := "codex://threads/new?path=D%3A%5Ctasks%5CDPF%20%E6%96%87%E4%BB%B6%E8%87%AA%E5%8A%A8%E8%A7%A3%E6%9E%90"
			err := open(context.Background(), uri, s)
			if (err != nil) != tt.fail || s.repairs != tt.repairs || s.opens != tt.opens {
				t.Fatal(err, s)
			}
			for _, actual := range s.uris {
				if actual != uri {
					t.Fatal("deep link changed", actual)
				}
			}
		})
	}
}

func TestOpenStopsBeforeChangesAndAfterCancellation(t *testing.T) {
	for _, stage := range []string{"already canceled", "no desktop", "write failure", "cancel after repair"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &fakeSystem{results: []error{errAssociationMissing, errAssociationMissing}}
			switch stage {
			case "already canceled":
				cancel()
			case "no desktop":
				s.discoverErr = errors.New("missing")
			case "write failure":
				s.repairErr = errors.New("denied")
			case "cancel after repair":
				s.onRepair = cancel
			}
			if err := open(ctx, "codex://threads/new?path=test", s); err == nil {
				t.Fatal("failure hidden")
			}
			if s.opens > 2 || stage == "no desktop" && s.repairs != 0 || stage == "already canceled" && s.opens != 0 {
				t.Fatal(s)
			}
		})
	}
}

type concurrentSystem struct {
	state             sync.Mutex
	repairLock        sync.Mutex
	repaired          bool
	repairs, accepted int
}

func (s *concurrentSystem) activate(context.Context, string) error {
	s.state.Lock()
	defer s.state.Unlock()
	if !s.repaired {
		return errAssociationMissing
	}
	s.accepted++
	return nil
}
func (s *concurrentSystem) discover(context.Context) (desktopCommand, error) {
	return desktopCommand{Executable: "desktop"}, nil
}
func (s *concurrentSystem) lock(context.Context) (func(), error) {
	s.repairLock.Lock()
	return s.repairLock.Unlock, nil
}
func (s *concurrentSystem) repair(context.Context, desktopCommand) error {
	s.state.Lock()
	defer s.state.Unlock()
	s.repaired = true
	s.repairs++
	return nil
}
func TestConcurrentOpenRepairsOnce(t *testing.T) {
	s := &concurrentSystem{}
	var group sync.WaitGroup
	for range 10 {
		group.Go(func() {
			if err := open(context.Background(), "codex://threads/new?path=test", s); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if s.repairs != 1 || s.accepted != 10 {
		t.Fatal(s.repairs, s.accepted)
	}
}
