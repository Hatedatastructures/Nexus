package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

// ─── Manifest parsing tests ───

func TestParseManifest_ValidMinimal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	content := "name: my-plugin\nversion: \"1.0.0\"\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	m, err := ParseManifest(path)
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}
	if m.Name != "my-plugin" {
		t.Errorf("Name = %q, want %q", m.Name, "my-plugin")
	}
	if m.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", m.Version, "1.0.0")
	}
}

func TestParseManifest_FullFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	content := `name: full-plugin
version: "2.3.1"
description: A full plugin
author: tester
license: MIT
kind: tool
platforms:
  - linux
  - darwin
provides_tools:
  - search
  - summarize
hooks:
  - pre_dispatch
external_deps:
  - ripgrep
requires_env:
  - API_KEY
entrypoint: ./plugin.so
config:
  timeout: 30
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	m, err := ParseManifest(path)
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}

	if m.Description != "A full plugin" {
		t.Errorf("Description = %q", m.Description)
	}
	if m.Kind != "tool" {
		t.Errorf("Kind = %q", m.Kind)
	}
	if len(m.Platforms) != 2 {
		t.Errorf("Platforms len = %d, want 2", len(m.Platforms))
	}
	if len(m.ProvidesTools) != 2 {
		t.Errorf("ProvidesTools len = %d, want 2", len(m.ProvidesTools))
	}
	if m.Entrypoint != "./plugin.so" {
		t.Errorf("Entrypoint = %q", m.Entrypoint)
	}
	if m.Config["timeout"] != 30 {
		t.Errorf("Config timeout = %v, want 30", m.Config["timeout"])
	}
}

func TestParseManifest_MissingName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	content := "version: \"1.0.0\"\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err := ParseManifest(path)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestParseManifest_MissingVersion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	content := "name: test\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err := ParseManifest(path)
	if err == nil {
		t.Fatal("expected error for missing version")
	}
}

func TestParseManifest_NameTooLong(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	longName := make([]byte, 65)
	for i := range longName {
		longName[i] = 'a'
	}
	content := "name: " + string(longName) + "\nversion: \"1.0.0\"\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err := ParseManifest(path)
	if err == nil {
		t.Fatal("expected error for name exceeding 64 chars")
	}
}

func TestParseManifest_InvalidKind(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	content := "name: test\nversion: \"1.0.0\"\nkind: unknown_kind\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err := ParseManifest(path)
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestParseManifest_InvalidYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	content := "{{invalid yaml:::\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err := ParseManifest(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParseManifest_FileNotFound(t *testing.T) {
	t.Parallel()

	_, err := ParseManifest("/nonexistent/path/plugin.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestParseManifest_ValidKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind string
	}{
		{kind: "tool"},
		{kind: "hook"},
		{kind: "memory"},
		{kind: "composite"},
	}

	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "plugin.yaml")
			content := "name: test-" + tc.kind + "\nversion: \"1.0.0\"\nkind: " + tc.kind + "\n"
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatalf("write manifest: %v", err)
			}

			m, err := ParseManifest(path)
			if err != nil {
				t.Fatalf("ParseManifest() kind=%q error = %v", tc.kind, err)
			}
			if m.Kind != tc.kind {
				t.Errorf("Kind = %q, want %q", m.Kind, tc.kind)
			}
		})
	}
}

// ─── ValidateManifest tests ───

func TestValidateManifest_Nil(t *testing.T) {
	t.Parallel()

	err := ValidateManifest(nil)
	if err == nil {
		t.Fatal("expected error for nil manifest")
	}
}

func TestValidateManifest_EmptyName(t *testing.T) {
	t.Parallel()

	m := &Manifest{Version: "1.0.0"}
	err := ValidateManifest(m)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestValidateManifest_EmptyVersion(t *testing.T) {
	t.Parallel()

	m := &Manifest{Name: "test"}
	err := ValidateManifest(m)
	if err == nil {
		t.Fatal("expected error for empty version")
	}
}

func TestValidateManifest_InvalidNameChars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{name: "has space"},
		{name: "has.dot"},
		{name: "has/slash"},
		{name: "has:symbol"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := &Manifest{Name: tc.name, Version: "1.0.0"}
			err := ValidateManifest(m)
			if err == nil {
				t.Fatalf("expected error for name %q", tc.name)
			}
		})
	}
}

func TestValidateManifest_ValidNameChars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{name: "simple"},
		{name: "with-hyphen"},
		{name: "with_underscore"},
		{name: "CamelCase"},
		{name: "with123numbers"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := &Manifest{Name: tc.name, Version: "1.0.0"}
			err := ValidateManifest(m)
			if err != nil {
				t.Fatalf("unexpected error for valid name %q: %v", tc.name, err)
			}
		})
	}
}
