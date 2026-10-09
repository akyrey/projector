package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/akyrey/projector/internal/config"
)

func TestInfo_RunsItemsInOrder(t *testing.T) {
	f := newFixture(t)
	dir := f.addProject(t, "api", &config.Config{Info: []config.InfoItem{
		{Name: "pwd", Description: "where", Cmd: config.NewStringOrList("basename \"$PWD\"")},
		{Name: "broken", Cmd: config.NewStringOrList("echo oops; exit 3")},
		{Name: "slow", Cmd: config.NewStringOrList("sleep 30")},
	}})

	start := time.Now()
	got, err := f.app.Info(context.Background(), dir, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Info took %v; the timeout should bound it", elapsed)
	}

	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}
	if got[0].Name != "pwd" || got[0].Description != "where" || got[0].Error != "" ||
		strings.TrimSpace(got[0].Output) == "" {
		t.Errorf("pwd = %+v", got[0])
	}
	if got[1].Name != "broken" || got[1].Error == "" || strings.TrimSpace(got[1].Output) != "oops" {
		t.Errorf("broken = %+v, want output oops and an error", got[1])
	}
	if got[2].Name != "slow" || !strings.Contains(got[2].Error, "timed out") {
		t.Errorf("slow = %+v, want a timeout error", got[2])
	}
}

func TestInfo_NoItems(t *testing.T) {
	f := newFixture(t)
	dir := f.addProject(t, "", &config.Config{})
	got, err := f.app.Info(context.Background(), dir, time.Second)
	if err != nil || len(got) != 0 {
		t.Errorf("Info = %v, %v; want empty", got, err)
	}
}

func TestInfo_OutputIsCapped(t *testing.T) {
	f := newFixture(t)
	dir := f.addProject(t, "", &config.Config{Info: []config.InfoItem{
		{Name: "noisy", Cmd: config.NewStringOrList("yes")},
	}})

	got, err := f.app.Info(context.Background(), dir, 2*time.Second)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if n := len(got[0].Output); n > 300<<10 {
		t.Errorf("output is %d bytes, want it capped", n)
	}
	if !strings.Contains(got[0].Output, "output truncated") {
		t.Errorf("output does not say it was truncated: ...%s", got[0].Output[len(got[0].Output)-80:])
	}
}
