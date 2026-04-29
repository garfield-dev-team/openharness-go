// Package skills 实现 Skills 的渐进式披露。
//
// 设计：
//  1. 扫描 ./.skills/<skill-name>/SKILL.md；
//  2. 在 system prompt 中仅列出 skill 名称 + 一句话描述（YAML front matter 的 description）；
//  3. 提供 `skill` 工具，参数为 name，调用后才返回该 SKILL.md 的完整正文，避免前置塞爆上下文。
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// Skill 单个 skill 的元信息。
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Body        string `json:"-"`
}

// Registry 进程内 skills 注册表。
type Registry struct {
	mu     sync.RWMutex
	skills []Skill
}

// LoadFrom 扫描 root 目录下的 SKILL.md。
func LoadFrom(root string) (*Registry, error) {
	r := &Registry{}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return r, nil // 不存在时返回空 registry，不报错
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillPath := filepath.Join(root, e.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		name, desc := parseFrontMatter(string(data))
		if name == "" {
			name = e.Name()
		}
		r.skills = append(r.skills, Skill{
			Name:        name,
			Description: desc,
			Path:        skillPath,
			Body:        string(data),
		})
	}
	sort.Slice(r.skills, func(i, j int) bool { return r.skills[i].Name < r.skills[j].Name })
	return r, nil
}

// List 返回当前所有 skills 的摘要。
func (r *Registry) List() []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Skill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, Skill{Name: s.Name, Description: s.Description, Path: s.Path})
	}
	return out
}

// Get 返回指定 skill 的完整正文。
func (r *Registry) Get(name string) (Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.skills {
		if strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	return Skill{}, false
}

// RenderCatalog 生成注入 system prompt 的 skills 目录（只有 name+desc）。
func (r *Registry) RenderCatalog() string {
	items := r.List()
	if len(items) == 0 {
		return "(no skills loaded)"
	}
	var b strings.Builder
	for _, s := range items {
		fmt.Fprintf(&b, "- **%s**: %s\n", s.Name, s.Description)
	}
	return b.String()
}

// parseFrontMatter 解析 SKILL.md 开头的 YAML-like front matter，提取 name/description。
// 为避免引入 yaml 依赖，使用朴素的行扫描。
func parseFrontMatter(body string) (name, desc string) {
	if !strings.HasPrefix(body, "---") {
		// fall back: 用第一行作为名字，第二行作为描述
		lines := strings.SplitN(body, "\n", 3)
		if len(lines) >= 1 {
			name = strings.TrimSpace(strings.TrimPrefix(lines[0], "#"))
		}
		if len(lines) >= 2 {
			desc = strings.TrimSpace(lines[1])
		}
		return
	}
	rest := body[3:]
	end := strings.Index(rest, "---")
	if end < 0 {
		return
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		}
		if strings.HasPrefix(line, "description:") {
			desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	return
}

// ---------------------------------------------------------------------------
// skill 工具：渐进式披露
// ---------------------------------------------------------------------------

type skillTool struct {
	reg *Registry
}

// NewSkillTool 创建 skill 工具。
func NewSkillTool(reg *Registry) tool.InvokableTool { return &skillTool{reg: reg} }

func (t *skillTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "skill",
		Desc: "加载并返回指定 skill 的完整指导文档。未列出的 skill 不要调用。调用后你必须严格按照文档中的步骤执行。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name": {Type: schema.String, Desc: "skill 名称", Required: true},
		}),
	}, nil
}

func (t *skillTool) InvokableRun(_ context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
		return string(b), nil
	}
	s, ok := t.reg.Get(args.Name)
	if !ok {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": fmt.Sprintf("skill %q not found", args.Name)})
		return string(b), nil
	}
	b, _ := json.Marshal(map[string]any{
		"ok":          true,
		"name":        s.Name,
		"description": s.Description,
		"body":        s.Body,
	})
	return string(b), nil
}
