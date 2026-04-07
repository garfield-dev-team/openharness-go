package skills

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Skill represents an AI agent skill loaded from a markdown file.
type Skill struct {
	Name         string
	Description  string
	Instructions string
}

// LoadSkills scans the given directory for SKILL.md files or *.md files
// representing skills, and returns a list of loaded skills.
func LoadSkills(dir string) ([]Skill, error) {
	var skills []Skill

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return skills, nil // no skills dir is fine
		}
		return nil, fmt.Errorf("skills: read dir %s: %w", dir, err)
	}

	for _, entry := range entries {
		// If it's a directory, check for SKILL.md inside it.
		// If it's a .md file, parse it directly.
		var path string
		if entry.IsDir() {
			path = filepath.Join(dir, entry.Name(), "SKILL.md")
			if _, err := os.Stat(path); err != nil {
				continue // no SKILL.md in this subdir
			}
		} else if strings.HasSuffix(entry.Name(), ".md") {
			path = filepath.Join(dir, entry.Name())
		} else {
			continue
		}

		skill, err := ParseSkillFile(path)
		if err != nil {
			// Warn or ignore? Let's just ignore for simplicity, or we could log it.
			continue
		}
		if skill.Name != "" {
			skills = append(skills, *skill)
		}
	}

	return skills, nil
}

// ParseSkillFile parses a markdown file with YAML-like frontmatter.
func ParseSkillFile(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return ParseSkillContent(data)
}

// ParseSkillContent extracts name, description, and instructions.
func ParseSkillContent(data []byte) (*Skill, error) {
	reader := bufio.NewReader(bytes.NewReader(data))
	var skill Skill

	// Check if starts with frontmatter "---"
	line, err := reader.ReadString('\n')
	if err != nil {
		if err == io.EOF {
			return &skill, nil
		}
		return nil, err
	}
	line = strings.TrimSpace(line)

	if line != "---" {
		// No frontmatter, treat whole file as instructions
		skill.Instructions = strings.TrimSpace(string(data))
		return &skill, nil
	}

	// Parse frontmatter
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			break // EOF or error
		}
		line = strings.TrimSpace(line)
		if line == "---" {
			break // End of frontmatter
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"'`) // strip quotes if any
			switch key {
			case "name":
				skill.Name = val
			case "description":
				skill.Description = val
			}
		}
	}

	// The rest is instructions
	var instructions bytes.Buffer
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				instructions.WriteString(line)
				break
			}
			break
		}
		instructions.WriteString(line)
	}

	skill.Instructions = strings.TrimSpace(instructions.String())
	return &skill, nil
}
