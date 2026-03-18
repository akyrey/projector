package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akyrey/projector/internal/config"
)

func writeDotEnv(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
}

func TestLoadDotEnv_MissingFile(t *testing.T) {
	m, err := config.LoadDotEnv(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestLoadDotEnv_BasicParsing(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, `
# comment line
FOO=bar
BAZ=qux

EMPTY=
`)

	m, err := config.LoadDotEnv(dir)
	if err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}

	cases := map[string]string{
		"FOO":   "bar",
		"BAZ":   "qux",
		"EMPTY": "",
	}
	for k, want := range cases {
		if got := m[k]; got != want {
			t.Errorf("key %q: want %q, got %q", k, want, got)
		}
	}
	if _, ok := m["# comment line"]; ok {
		t.Error("comment line should not be parsed as a key")
	}
}

func TestLoadDotEnv_QuotedValues(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, `
DOUBLE="hello world"
SINGLE='foo bar'
UNQUOTED=plain
`)

	m, err := config.LoadDotEnv(dir)
	if err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}

	if got := m["DOUBLE"]; got != "hello world" {
		t.Errorf("DOUBLE: want %q, got %q", "hello world", got)
	}
	if got := m["SINGLE"]; got != "foo bar" {
		t.Errorf("SINGLE: want %q, got %q", "foo bar", got)
	}
	if got := m["UNQUOTED"]; got != "plain" {
		t.Errorf("UNQUOTED: want %q, got %q", "plain", got)
	}
}

func TestLoadDotEnv_InvalidLine(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, "NODEQUAL\n")

	_, err := config.LoadDotEnv(dir)
	if err == nil {
		t.Error("expected error for line without '=', got nil")
	}
}

func TestMergeEnv_OverridePrecedence(t *testing.T) {
	base := map[string]string{"A": "base-a", "B": "base-b"}
	override := map[string]string{"B": "override-b", "C": "override-c"}

	result := config.MergeEnv(base, override)

	if result["A"] != "base-a" {
		t.Errorf("A: want base-a, got %q", result["A"])
	}
	if result["B"] != "override-b" {
		t.Errorf("B: want override-b (override wins), got %q", result["B"])
	}
	if result["C"] != "override-c" {
		t.Errorf("C: want override-c, got %q", result["C"])
	}
}
