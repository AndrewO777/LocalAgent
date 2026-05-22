package skills

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParse_Minimal(t *testing.T) {
	in := []byte("---\nname: caveman\ndescription: be terse\n---\n\n# Body here\n")
	sk, err := parse(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sk.Name != "caveman" {
		t.Errorf("name: got %q want caveman", sk.Name)
	}
	if sk.Description != "be terse" {
		t.Errorf("description: got %q want 'be terse'", sk.Description)
	}
	if !strings.Contains(sk.Body, "# Body here") {
		t.Errorf("body missing: %q", sk.Body)
	}
}

func TestParse_MultilineDescription(t *testing.T) {
	// WNP-Loop's actual description spans many lines as a YAML flowed scalar.
	in := []byte(`---
name: wnp-loop
description: Use the WNP Loop methodology for sustained AI-assisted software
  development with solo developers or small teams. Trigger when the user asks to
  work in a What's Next/Proceed cadence.
---

The body.
`)
	sk, err := parse(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(sk.Description, "WNP Loop methodology") {
		t.Errorf("description start missing: %q", sk.Description)
	}
	if !strings.Contains(sk.Description, "What's Next/Proceed cadence") {
		t.Errorf("description end missing: %q", sk.Description)
	}
	if strings.Contains(sk.Description, "\n") {
		t.Errorf("multiline description should be folded, got newline: %q", sk.Description)
	}
}

func TestParse_NoFrontmatter(t *testing.T) {
	_, err := parse([]byte("# just markdown\nno frontmatter"))
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParse_MissingName(t *testing.T) {
	_, err := parse([]byte("---\ndescription: no name here\n---\nbody"))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestParse_InvalidName(t *testing.T) {
	cases := []string{
		"---\nname: Has-Capitals\ndescription: x\n---\n",
		"---\nname: under_score\ndescription: x\n---\n",
		"---\nname: 1starts-with-digit\ndescription: x\n---\n",
		"---\nname: has space\ndescription: x\n---\n",
	}
	for _, c := range cases {
		if _, err := parse([]byte(c)); err == nil {
			t.Errorf("expected error for invalid name in: %q", c)
		}
	}
}

func TestParseSlashCommands(t *testing.T) {
	cat := &Catalog{
		byName: map[string]*Skill{
			"wnp-loop": {Name: "wnp-loop"},
			"caveman":  {Name: "caveman"},
		},
		order: []string{"wnp-loop", "caveman"},
	}

	cases := []struct {
		name      string
		goal      string
		wantGoal  string
		wantNames []string
	}{
		{
			name:      "no slashes",
			goal:      "do the thing",
			wantGoal:  "do the thing",
			wantNames: nil,
		},
		{
			name:      "single skill",
			goal:      "/caveman do the thing",
			wantGoal:  "do the thing",
			wantNames: []string{"caveman"},
		},
		{
			name:      "two skills stacked",
			goal:      "/wnp-loop /caveman do the thing",
			wantGoal:  "do the thing",
			wantNames: []string{"wnp-loop", "caveman"},
		},
		{
			name:      "unknown command stays in goal",
			goal:      "/not-a-skill do the thing",
			wantGoal:  "/not-a-skill do the thing",
			wantNames: nil,
		},
		{
			name:      "known then unknown stops at unknown",
			goal:      "/caveman /not-a-skill do the thing",
			wantGoal:  "/not-a-skill do the thing",
			wantNames: []string{"caveman"},
		},
		{
			name:      "duplicate names deduped",
			goal:      "/caveman /caveman do it",
			wantGoal:  "do it",
			wantNames: []string{"caveman"},
		},
		{
			name:      "leading whitespace tolerated",
			goal:      "  /caveman go",
			wantGoal:  "go",
			wantNames: []string{"caveman"},
		},
		{
			name:      "slash without trailing space at end of string",
			goal:      "/caveman",
			wantGoal:  "",
			wantNames: []string{"caveman"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotGoal, gotNames := ParseSlashCommands(tc.goal, cat)
			if gotGoal != tc.wantGoal {
				t.Errorf("goal: got %q want %q", gotGoal, tc.wantGoal)
			}
			if !reflect.DeepEqual(gotNames, tc.wantNames) {
				t.Errorf("names: got %v want %v", gotNames, tc.wantNames)
			}
		})
	}
}

func TestDiscover_ProjectOverridesUser(t *testing.T) {
	// Project-local skill with the same name as a user-global one should
	// win. We exercise this by pointing HOME at a temp dir alongside a
	// project skills dir.
	t.Setenv("HOME", filepath.Join(t.TempDir(), "fake-home"))
	t.Setenv("USERPROFILE", filepath.Join(t.TempDir(), "fake-home")) // Windows
	home, _ := os.UserHomeDir()
	if home == "" {
		t.Skip("could not set HOME")
	}

	writeSkill(t, filepath.Join(home, ".localagent", "skills", "shared", "SKILL.md"),
		"---\nname: shared\ndescription: from user\n---\nuser body")

	workdir := filepath.Join(t.TempDir(), "proj")
	writeSkill(t, filepath.Join(workdir, ".localagent", "skills", "shared", "SKILL.md"),
		"---\nname: shared\ndescription: from project\n---\nproject body")

	cat, _, err := Discover(workdir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	sk := cat.Get("shared")
	if sk == nil {
		t.Fatal("shared skill missing")
	}
	if sk.Source != SourceProject {
		t.Errorf("source: got %q want %q", sk.Source, SourceProject)
	}
	if !strings.Contains(sk.Body, "project body") {
		t.Errorf("body: project version expected, got %q", sk.Body)
	}
}

func TestDiscover_MalformedSkillDoesNotBreakOthers(t *testing.T) {
	workdir := t.TempDir()
	writeSkill(t, filepath.Join(workdir, ".localagent", "skills", "good", "SKILL.md"),
		"---\nname: good\ndescription: works\n---\nbody")
	writeSkill(t, filepath.Join(workdir, ".localagent", "skills", "bad", "SKILL.md"),
		"no frontmatter at all")

	cat, warns, err := Discover(workdir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if cat.Get("good") == nil {
		t.Error("good skill should still be discovered")
	}
	if len(warns) == 0 {
		t.Error("expected at least one warning for the malformed skill")
	}
}

func writeSkill(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
