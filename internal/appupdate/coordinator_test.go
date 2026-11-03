package appupdate_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"silverwing-go/internal/appupdate"
)

func installation(t *testing.T) appupdate.Installation {
	t.Helper()
	root := filepath.Join(t.TempDir(), "program")
	os.MkdirAll(filepath.Join(root, "resources"), 0o700)
	os.WriteFile(filepath.Join(root, "app.exe"), []byte("old app"), 0o700)
	os.WriteFile(filepath.Join(root, "helper.exe"), []byte("old helper"), 0o700)
	identity := appupdate.Identity{Repository: "owner/repo", TagPrefix: "v", Executable: "app", Platform: "windows/amd64", Version: "1.0.0"}
	files, err := appupdate.Inventory(root, "resources/update-installation.json")
	if err != nil {
		t.Fatal(err)
	}
	layout := appupdate.Layout{Schema: 1, Identity: identity, Executable: "app.exe", Helper: "helper.exe", Files: files}
	marker := filepath.Join(root, "resources/update-installation.json")
	if err := appupdate.WriteJSON(marker, layout); err != nil {
		t.Fatal(err)
	}
	return appupdate.Installation{Identity: identity, Kind: appupdate.Portable, Root: root, Executable: filepath.Join(root, "app.exe"), Helper: filepath.Join(root, "helper.exe"), LayoutPath: marker, Layout: layout, Home: t.TempDir()}
}

type fixtureSource struct {
	mu       sync.Mutex
	versions []string
	wait     <-chan struct{}
}

func (s *fixtureSource) Prepare(ctx context.Context, i appupdate.Installation, version, destination string, progress appupdate.Progress) (appupdate.Bundle, error) {
	s.mu.Lock()
	s.versions = append(s.versions, version)
	s.mu.Unlock()
	if s.wait != nil {
		select {
		case <-ctx.Done():
			return appupdate.Bundle{}, ctx.Err()
		case <-s.wait:
		}
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return appupdate.Bundle{}, err
	}
	if progress != nil {
		progress(50, 100)
	}
	identity := i.Identity
	identity.Version = version
	return appupdate.Bundle{Directory: destination, Archive: filepath.Join(destination, "archive"), Metadata: appupdate.Metadata{Identity: identity, Kind: i.Kind}}, nil
}

func TestCoordinatorKeepsPreparationSeparateAndRetainsRecoveryIdentity(t *testing.T) {
	for _, kind := range []appupdate.Kind{appupdate.Portable, appupdate.Setup, appupdate.MSI, appupdate.MacApp} {
		i := installation(t)
		i.Kind = kind
		source := &fixtureSource{}
		coordinator := appupdate.New(source, filepath.Join(i.Home, "updates"))
		result, err := coordinator.Prepare(context.Background(), i, "1.0.1")
		if err != nil || result.Phase != "ready" || result.ID == "" {
			t.Fatalf("%s: %+v %v", kind, result, err)
		}
		plan, err := coordinator.Plan(result.ID)
		if err != nil {
			t.Fatal(err)
		}
		if plan.ParentPID != os.Getpid() || plan.Installation.Root != i.Root || plan.ExecutableDigest == "" {
			t.Fatal("handoff lost installation identity")
		}
		needsRecovery := kind == appupdate.Setup || kind == appupdate.MSI
		if (plan.Recovery != nil) != needsRecovery {
			t.Fatal("registered install must retain a verified recovery installer")
		}
		if content, _ := os.ReadFile(i.Executable); string(content) != "old app" {
			t.Fatal("prepare wrote installed program")
		}
		coordinator.Cancel()
		if _, err := coordinator.Plan(result.ID); err == nil {
			t.Fatal("cancelled handoff remained installable")
		}
	}
}

func TestCoordinatorRejectsDowngradeCancellationAndChangedFiles(t *testing.T) {
	i := installation(t)
	coordinator := appupdate.New(&fixtureSource{}, filepath.Join(i.Home, "updates"))
	if _, err := coordinator.Prepare(context.Background(), i, "0.9.0"); err == nil {
		t.Fatal("downgrade accepted")
	}
	os.WriteFile(i.Executable, []byte("changed"), 0o700)
	if _, err := coordinator.Prepare(context.Background(), i, "1.0.1"); err == nil {
		t.Fatal("modified application accepted")
	}
	i = installation(t)
	source := &fixtureSource{wait: make(chan struct{})}
	coordinator = appupdate.New(source, filepath.Join(i.Home, "updates"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := coordinator.Prepare(ctx, i, "1.0.1")
	if !errors.Is(err, context.Canceled) || coordinator.Status().Phase != "cancelled" {
		t.Fatalf("cancelled job: %+v %v", coordinator.Status(), err)
	}
}

func TestUpdatePathsRejectTraversalAndSpecialFiles(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"../escape", "/absolute", `C:\outside`, "a/../../escape", ".", ""} {
		if _, err := appupdate.Resolve(root, path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "redirect")); err == nil {
		if _, err := appupdate.Resolve(root, "redirect/file"); err == nil {
			t.Fatal("symlink traversal accepted")
		}
	}
}

func TestCoordinatorOldCancelDoesNotCancelNewReadyJob(t *testing.T) {
	i := installation(t)
	c := appupdate.New(&fixtureSource{}, filepath.Join(i.Home, "updates"))
	first, err := c.Prepare(context.Background(), i, "1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	c.CancelJob(first.ID)
	next, err := c.Prepare(context.Background(), i, "1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CancelJob(first.ID); err == nil {
		t.Fatal("stale cancellation accepted")
	}
	if _, err := c.Plan(next.ID); err != nil || c.Status().Phase != "ready" {
		t.Fatal("stale cancellation changed current job", err)
	}
}
