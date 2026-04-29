// Package tools 提供 einoagent 使用的基础文件系统工具。
// 所有工具均实现 eino 的 tool.InvokableTool 接口。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ---------------------------------------------------------------------------
// 通用辅助
// ---------------------------------------------------------------------------

// resolvePath 将相对路径转换为工作目录下的绝对路径，并做基础校验。
func resolvePath(cwd, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

type jsonToolResult struct {
	OK     bool   `json:"ok"`
	Data   any    `json:"data,omitempty"`
	Error  string `json:"error,omitempty"`
	Path   string `json:"path,omitempty"`
}

func okResult(data any) string {
	b, _ := json.Marshal(jsonToolResult{OK: true, Data: data})
	return string(b)
}

func errResult(err error) string {
	b, _ := json.Marshal(jsonToolResult{OK: false, Error: err.Error()})
	return string(b)
}

// ---------------------------------------------------------------------------
// ls 工具
// ---------------------------------------------------------------------------

type lsTool struct {
	cwd string
}

func (t *lsTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "ls",
		Desc: "列出指定目录下的文件和子目录（非递归）。返回 JSON。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"path": {
				Type:     schema.String,
				Desc:     "目标目录路径，可以是相对路径或绝对路径；留空表示当前工作目录。",
				Required: false,
			},
		}),
	}, nil
}

func (t *lsTool) InvokableRun(_ context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	target := args.Path
	if target == "" {
		target = t.cwd
	}
	abs, err := resolvePath(t.cwd, target)
	if err != nil {
		return errResult(err), nil
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return errResult(err), nil
	}
	names := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		names = append(names, map[string]any{
			"name":  e.Name(),
			"isDir": e.IsDir(),
		})
	}
	sort.Slice(names, func(i, j int) bool {
		return names[i]["name"].(string) < names[j]["name"].(string)
	})
	return okResult(map[string]any{"path": abs, "entries": names}), nil
}

// NewLsTool 创建 ls 工具。
func NewLsTool(cwd string) tool.InvokableTool { return &lsTool{cwd: cwd} }

// ---------------------------------------------------------------------------
// read 工具
// ---------------------------------------------------------------------------

type readTool struct {
	cwd        string
	maxBytes   int64
}

func (t *readTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "read_file",
		Desc: "读取指定文件的内容（按行范围），单次最多返回若干行，防止上下文爆炸。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"path":   {Type: schema.String, Desc: "文件路径", Required: true},
			"offset": {Type: schema.Integer, Desc: "起始行（从 1 开始），默认 1"},
			"limit":  {Type: schema.Integer, Desc: "最多读取的行数，默认 400"},
		}),
	}, nil
}

func (t *readTool) InvokableRun(_ context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return errResult(err), nil
	}
	if args.Limit <= 0 {
		args.Limit = 400
	}
	if args.Offset <= 0 {
		args.Offset = 1
	}
	abs, err := resolvePath(t.cwd, args.Path)
	if err != nil {
		return errResult(err), nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return errResult(err), nil
	}
	if t.maxBytes > 0 && int64(len(data)) > t.maxBytes {
		data = data[:t.maxBytes]
	}
	lines := strings.Split(string(data), "\n")
	start := args.Offset - 1
	if start > len(lines) {
		start = len(lines)
	}
	end := start + args.Limit
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		b.WriteString(fmt.Sprintf("%6d\t%s\n", i+1, lines[i]))
	}
	return okResult(map[string]any{
		"path":        abs,
		"from_line":   start + 1,
		"to_line":     end,
		"total_lines": len(lines),
		"content":     b.String(),
	}), nil
}

// NewReadTool 创建 read_file 工具。
func NewReadTool(cwd string) tool.InvokableTool {
	return &readTool{cwd: cwd, maxBytes: 2 * 1024 * 1024}
}

// ---------------------------------------------------------------------------
// write 工具
// ---------------------------------------------------------------------------

type writeTool struct {
	cwd string
}

func (t *writeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "write_file",
		Desc: "将完整内容写入到指定文件（覆盖模式），会自动创建父目录。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"path":    {Type: schema.String, Desc: "目标文件路径", Required: true},
			"content": {Type: schema.String, Desc: "要写入的完整内容", Required: true},
		}),
	}, nil
}

func (t *writeTool) InvokableRun(_ context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return errResult(err), nil
	}
	abs, err := resolvePath(t.cwd, args.Path)
	if err != nil {
		return errResult(err), nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return errResult(err), nil
	}
	if err := os.WriteFile(abs, []byte(args.Content), 0o644); err != nil {
		return errResult(err), nil
	}
	return okResult(map[string]any{"path": abs, "bytes": len(args.Content)}), nil
}

// NewWriteTool 创建 write_file 工具。
func NewWriteTool(cwd string) tool.InvokableTool { return &writeTool{cwd: cwd} }

// ---------------------------------------------------------------------------
// edit 工具（字符串替换，精准修改）
// ---------------------------------------------------------------------------

type editTool struct {
	cwd string
}

func (t *editTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "edit_file",
		Desc: "对指定文件做精确的 search/replace 修改。old_str 必须在文件中唯一出现，否则会拒绝修改。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"path":    {Type: schema.String, Desc: "目标文件路径", Required: true},
			"old_str": {Type: schema.String, Desc: "需要被替换的原始字符串（必须唯一）", Required: true},
			"new_str": {Type: schema.String, Desc: "替换后的字符串", Required: true},
		}),
	}, nil
}

func (t *editTool) InvokableRun(_ context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Path   string `json:"path"`
		OldStr string `json:"old_str"`
		NewStr string `json:"new_str"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return errResult(err), nil
	}
	abs, err := resolvePath(t.cwd, args.Path)
	if err != nil {
		return errResult(err), nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return errResult(err), nil
	}
	content := string(data)
	count := strings.Count(content, args.OldStr)
	if count == 0 {
		return errResult(fmt.Errorf("old_str not found in file")), nil
	}
	if count > 1 {
		return errResult(fmt.Errorf("old_str is not unique (%d matches); expand context to make it unique", count)), nil
	}
	updated := strings.Replace(content, args.OldStr, args.NewStr, 1)
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return errResult(err), nil
	}
	return okResult(map[string]any{"path": abs, "replaced": true}), nil
}

// NewEditTool 创建 edit_file 工具。
func NewEditTool(cwd string) tool.InvokableTool { return &editTool{cwd: cwd} }
