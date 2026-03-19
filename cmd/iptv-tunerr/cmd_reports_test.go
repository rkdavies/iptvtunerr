package main

import (
	"testing"

	"github.com/snapetech/iptvtunerr/internal/tuner"
)

func TestMaybeRunGhostHunterRecovery(t *testing.T) {
	oldRunner := ghostHunterRecoverRunner
	defer func() { ghostHunterRecoverRunner = oldRunner }()

	called := ""
	ghostHunterRecoverRunner = func(mode string) error {
		called = mode
		return nil
	}

	if err := maybeRunGhostHunterRecovery(tuner.GhostHunterReport{HiddenGrabSuspected: false}, "dry-run"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if called != "" {
		t.Fatalf("called=%q want empty", called)
	}
	if err := maybeRunGhostHunterRecovery(tuner.GhostHunterReport{HiddenGrabSuspected: true}, "restart"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if called != "restart" {
		t.Fatalf("called=%q want restart", called)
	}
}
