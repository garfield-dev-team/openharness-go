package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSkillFile(t *testing.T) {
	content := `---
name: test-skill
description: A test skill
---
This is a test instruction.
It has multiple lines.
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.md")
	os.WriteFile(path, []byte(content), 0644)

	skill, err := ParseSkillFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if skill.Name != "test-skill" {
		t.Errorf("expected name 'test-skill', got '%s'", skill.Name)
	}
	if skill.Description != "A test skill" {
		t.Errorf("expected description 'A test skill', got '%s'", skill.Description)
	}
	if skill.Instructions != "This is a test instruction.\nIt has multiple lines." {
		t.Errorf("expected instructions, got '%s'", skill.Instructions)
	}
}

func TestLoadSkills(t *testing.T) {
	tmpDir := t.TempDir()

	// skill 1: in root
	os.WriteFile(filepath.Join(tmpDir, "skill1.md"), []byte("---\nname: s1\ndescription: d1\n---\ni1"), 0644)

	// skill 2: in subdirectory as SKILL.md
	os.MkdirAll(filepath.Join(tmpDir, "s2-dir"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "s2-dir", "SKILL.md"), []byte("---\nname: s2\ndescription: d2\n---\ni2"), 0644)

	skills, err := LoadSkills(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}

	foundS1 := false
	foundS2 := false
	for _, s := range skills {
		if s.Name == "s1" {
			foundS1 = true
		}
		if s.Name == "s2" {
			foundS2 = true
		}
	}

	if !foundS1 || !foundS2 {
		t.Errorf("did not find both skills: %+v", skills)
	}
}
