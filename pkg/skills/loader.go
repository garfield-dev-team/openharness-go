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
	IsSubSkill   bool // If true, it won't be shown in the top-level <available_skills> index
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

// LoadPlugins scans a plugins directory, where each plugin has a 'skills' subdirectory.
// It creates a "virtual skill" for the plugin itself that lists its sub-skills,
// and prefixes the sub-skill names with the plugin name (e.g. 'superpowers:writing-plans').
func LoadPlugins(pluginsDir string) ([]Skill, error) {
	var allSkills []Skill

	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return allSkills, nil
		}
		return nil, fmt.Errorf("plugins: read dir %s: %w", pluginsDir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pluginName := entry.Name()
		pluginSkillsDir := filepath.Join(pluginsDir, pluginName, "skills")

		pluginSkills, err := LoadSkills(pluginSkillsDir)
		if err != nil {
			continue // skip unreadable plugins
		}

		if len(pluginSkills) == 0 {
			continue
		}

		// Create a virtual skill for the plugin index
		var pluginDescription strings.Builder
		pluginDescription.WriteString(fmt.Sprintf("A plugin collection of %d skills. Call this skill to see the list of available sub-skills.", len(pluginSkills)))

		var pluginInstructions strings.Builder
		pluginInstructions.WriteString(fmt.Sprintf("The plugin '%s' contains the following skills:\n\n", pluginName))

		for _, s := range pluginSkills {
			// Prefix the skill name with the plugin name to avoid collisions
			prefixedName := fmt.Sprintf("%s:%s", pluginName, s.Name)
			
			pluginInstructions.WriteString(fmt.Sprintf("- **%s**: %s\n", prefixedName, s.Description))
			
			s.Name = prefixedName
			// For the individual sub-skills, we prepend a small header
			// so the model knows it loaded a sub-skill
			s.Instructions = fmt.Sprintf("[Sub-skill loaded from plugin '%s']\n\n%s", pluginName, s.Instructions)
			s.IsSubSkill = true
			
			allSkills = append(allSkills, s)
		}

		allSkills = append(allSkills, Skill{
			Name:         pluginName,
			Description:  pluginDescription.String(),
			Instructions: pluginInstructions.String(),
			IsSubSkill:   false, // This is the top-level index, so it IS visible
		})
	}

	return allSkills, nil
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
