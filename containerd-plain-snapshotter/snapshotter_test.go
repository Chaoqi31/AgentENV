package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/containerd/containerd/errdefs"
)

func newTestSnapshotter(t *testing.T) *Snapshotter {
	t.Helper()
	s, err := NewSnapshotter(t.TempDir())
	if err != nil {
		t.Fatalf("NewSnapshotter: %v", err)
	}
	return s
}

func TestMalformedConfigIsReportedAndKept(t *testing.T) {
	for name, content := range map[string]string{
		"array":     "[]",
		"null":      "null",
		"empty":     "",
		"truncated": `{"sha256:aaa": "/rootfs/a"`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := newTestSnapshotter(t)
			if err := s.RegisterRootfs("sha256:aaa", "/rootfs/a"); err != nil {
				t.Fatalf("RegisterRootfs: %v", err)
			}
			if err := os.WriteFile(s.configPath(), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}

			if _, err := NewSnapshotter(s.root); err == nil || !strings.Contains(err.Error(), s.configPath()) {
				t.Errorf("NewSnapshotter error = %v, want an error naming %s", err, s.configPath())
			}
			if err := s.RegisterRootfs("sha256:bbb", "/rootfs/b"); err == nil || !strings.Contains(err.Error(), s.configPath()) {
				t.Errorf("RegisterRootfs error = %v, want an error naming %s", err, s.configPath())
			}
			if _, err := s.Stat(ctx, "sha256:aaa"); err == nil || errdefs.IsNotFound(err) {
				t.Errorf("Stat error = %v, want the config error rather than not found", err)
			}

			got, err := os.ReadFile(s.configPath())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, []byte(content)) {
				t.Errorf("config.json was rewritten to %q", got)
			}
		})
	}
}

func TestConcurrentRegistrationsKeepEveryEntry(t *testing.T) {
	root := t.TempDir()
	const n = 32
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Separate instances stand in for separate `register` processes.
			s, err := NewSnapshotter(root)
			if err == nil {
				err = s.RegisterRootfs(fmt.Sprintf("sha256:%02d", i), fmt.Sprintf("/rootfs/%02d", i))
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	config, err := (&Snapshotter{root: root}).getConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(config) != n {
		t.Errorf("config.json has %d of %d registrations", len(config), n)
	}
}

func TestPrepareWithUnknownParentIsNotFound(t *testing.T) {
	s := newTestSnapshotter(t)

	_, err := s.Prepare(context.Background(), "moby/8/container", "moby/7/sha256:unregistered")
	if !errdefs.IsNotFound(err) {
		t.Fatalf("Prepare error = %v, want not found", err)
	}
	if dirs, _ := filepath.Glob(filepath.Join(s.root, "snap-*")); len(dirs) != 0 {
		t.Errorf("Prepare left temp dirs %v", dirs)
	}
}
