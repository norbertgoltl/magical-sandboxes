package main

import (
	"path/filepath"
	"testing"
)

func TestValidateSandboxProjectPathRejectsOverlap(t *testing.T) {
	root := filepath.Clean("/Users/test/Work/magical-sandboxes")
	cases := []struct {
		name    string
		project string
	}{
		{"same path", root},
		{"ancestor", filepath.Dir(root)},
		{"descendant", filepath.Join(root, "examples", "demo")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateSandboxProjectPath(tc.project, root); err == nil {
				t.Fatalf("expected overlap to be rejected: project=%q root=%q", tc.project, root)
			}
		})
	}
}

func TestValidateSandboxProjectPathAllowsSeparateTree(t *testing.T) {
	root := filepath.Clean("/Users/test/Work/magical-sandboxes")
	project := filepath.Clean("/Users/test/Projects/example-app")
	if err := validateSandboxProjectPath(project, root); err != nil {
		t.Fatalf("expected separate project tree to be allowed: %v", err)
	}
}

func TestPathContainsUsesPathBoundaries(t *testing.T) {
	if pathContains("/tmp/foo", "/tmp/foobar") {
		t.Fatal("prefix-only path must not count as containment")
	}
	if !pathContains("/tmp/foo", "/tmp/foo/bar") {
		t.Fatal("child path should count as containment")
	}
}
