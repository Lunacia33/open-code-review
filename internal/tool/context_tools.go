package tool

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	contextSearchMaxCount = 30
	contextReadMaxLines   = 120
)

type ContextToolProvider struct {
	ToolID  Tool
	Backend QueryBackend
}

func NewContextTool(id Tool, backend QueryBackend) *ContextToolProvider {
	return &ContextToolProvider{ToolID: id, Backend: backend}
}

func (p *ContextToolProvider) Tool() Tool { return p.ToolID }

func (p *ContextToolProvider) Execute(ctx context.Context, args map[string]any) (string, error) {
	if p.Backend == nil {
		return "Error: context tool backend is not configured", nil
	}
	switch p.ToolID {
	case SymbolDefinition:
		return p.symbolDefinition(ctx, args)
	case SymbolReferences:
		return p.symbolReferences(ctx, args)
	case CallGraph:
		return p.callGraph(ctx, args)
	case CppDeclContext:
		return p.cppDeclContext(ctx, args)
	case UEAssetRefs:
		return p.ueAssetRefs(ctx, args)
	case ThreadContext:
		return p.threadContext(ctx, args)
	case LuaStateContext:
		return p.luaStateContext(ctx, args)
	default:
		return "Error: unsupported context tool", nil
	}
}

func (p *ContextToolProvider) symbolDefinition(ctx context.Context, args map[string]any) (string, error) {
	symbol := strings.TrimSpace(stringArg(args, "symbol"))
	if symbol == "" {
		return "Error: symbol is required", nil
	}
	patterns := scopePatterns(args)
	searches := []string{
		fmt.Sprintf(`\b(function|func|class|struct|enum|interface)\s+%s\b`, regexp.QuoteMeta(symbol)),
		fmt.Sprintf(`\b%s\s*[=:]\s*function\b`, regexp.QuoteMeta(symbol)),
		fmt.Sprintf(`\b%s\s*\(`, regexp.QuoteMeta(symbol)),
	}
	var sections []string
	for _, query := range searches {
		result, cost, err := p.Backend.Search(ctx, query, true, true, patterns, contextSearchMaxCount)
		if err != nil {
			return "", err
		}
		if hasMatches(result) {
			sections = append(sections, formatContextSection("definition_candidates", query, patterns, result, cost))
			break
		}
		sections = append(sections, formatContextSection("definition_search_miss", query, patterns, result, cost))
	}
	return contextToolOutput("symbol_definition", symbol, sections), nil
}

func (p *ContextToolProvider) symbolReferences(ctx context.Context, args map[string]any) (string, error) {
	symbol := strings.TrimSpace(stringArg(args, "symbol"))
	if symbol == "" {
		return "Error: symbol is required", nil
	}
	patterns := scopePatterns(args)
	result, cost, err := p.Backend.Search(ctx, symbol, true, false, patterns, contextSearchMaxCount)
	if err != nil {
		return "", err
	}
	return contextToolOutput("symbol_references", symbol, []string{
		formatContextSection("references", symbol, patterns, result, cost),
	}), nil
}

func (p *ContextToolProvider) callGraph(ctx context.Context, args map[string]any) (string, error) {
	function := strings.TrimSpace(firstNonEmpty(stringArg(args, "function"), stringArg(args, "symbol")))
	if function == "" {
		return "Error: function is required", nil
	}
	patterns := scopePatterns(args)
	defQuery := fmt.Sprintf(`\b(function|func)\s+%s\b|\b%s\s*[=:]\s*function\b`, regexp.QuoteMeta(function), regexp.QuoteMeta(function))
	callQuery := fmt.Sprintf(`\b%s\s*\(`, regexp.QuoteMeta(function))

	defs, defCost, err := p.Backend.Search(ctx, defQuery, true, true, patterns, contextSearchMaxCount)
	if err != nil {
		return "", err
	}
	calls, callCost, err := p.Backend.Search(ctx, callQuery, true, true, patterns, contextSearchMaxCount)
	if err != nil {
		return "", err
	}
	return contextToolOutput("call_graph", function, []string{
		formatContextSection("definition_candidates", defQuery, patterns, defs, defCost),
		formatContextSection("caller_or_callee_candidates", callQuery, patterns, calls, callCost),
	}), nil
}

func (p *ContextToolProvider) cppDeclContext(ctx context.Context, args map[string]any) (string, error) {
	path := strings.TrimSpace(firstNonEmpty(stringArg(args, "path"), stringArg(args, "file_path")))
	symbol := strings.TrimSpace(stringArg(args, "symbol"))
	if path == "" && symbol == "" {
		return "Error: path or symbol is required", nil
	}

	var sections []string
	var candidates []string
	if path != "" {
		candidates = append(candidates, cppHeaderCandidates(path)...)
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		lines, total, cost, err := p.Backend.ReadLines(ctx, candidate, 1, contextReadMaxLines)
		if err != nil {
			sections = append(sections, fmt.Sprintf("## declaration_file_miss\nfile: %s\nerror: %v\nQUERY_COST: %+v", candidate, err, cost))
			continue
		}
		body := strings.Join(lines, "\n")
		if symbol != "" && !strings.Contains(body, symbol) {
			sections = append(sections, fmt.Sprintf("## declaration_file_read\nfile: %s\ntotal_lines: %d\ncontains_symbol: false\nQUERY_COST: %+v", candidate, total, cost))
			continue
		}
		sections = append(sections, fmt.Sprintf("## declaration_file_read\nfile: %s\ntotal_lines: %d\nQUERY_COST: %+v\n%s", candidate, total, cost, body))
	}

	if symbol != "" {
		patterns := scopePatterns(args)
		if len(patterns) == 0 && path != "" {
			patterns = []string{dirScope(path)}
		}
		result, cost, err := p.Backend.Search(ctx, symbol, true, false, patterns, contextSearchMaxCount)
		if err != nil {
			return "", err
		}
		sections = append(sections, formatContextSection("symbol_in_decl_scope", symbol, patterns, result, cost))
	}

	return contextToolOutput("cpp_decl_context", firstNonEmpty(symbol, path), sections), nil
}

func (p *ContextToolProvider) ueAssetRefs(ctx context.Context, args map[string]any) (string, error) {
	name := strings.TrimSpace(firstNonEmpty(stringArg(args, "name"), stringArg(args, "symbol")))
	if name == "" {
		return "Error: name is required", nil
	}
	patterns := scopePatterns(args)
	if len(patterns) == 0 {
		patterns = []string{"*.json", "*.ini", "*.uasset.txt", "*.asset.json"}
	}
	result, cost, err := p.Backend.Search(ctx, name, true, false, patterns, contextSearchMaxCount)
	if err != nil {
		return "", err
	}
	return contextToolOutput("ue_asset_refs", name, []string{
		formatContextSection("asset_reference_candidates", name, patterns, result, cost),
	}), nil
}

func (p *ContextToolProvider) threadContext(ctx context.Context, args map[string]any) (string, error) {
	symbol := strings.TrimSpace(firstNonEmpty(stringArg(args, "symbol"), stringArg(args, "path"), stringArg(args, "file_path")))
	if symbol == "" {
		return "Error: symbol or path is required", nil
	}
	patterns := scopePatterns(args)
	keywords := []string{"Async", "Task", "Thread", "Timer", "Lock", "Mutex", "Destroy", "Unregister", "Cancel", "Weak"}
	var sections []string
	for _, keyword := range keywords {
		query := keyword
		result, cost, err := p.Backend.Search(ctx, query, false, false, patterns, 10)
		if err != nil {
			return "", err
		}
		if hasMatches(result) {
			sections = append(sections, formatContextSection("thread_lifecycle_"+strings.ToLower(keyword), query, patterns, result, cost))
		}
	}
	if len(sections) == 0 {
		sections = append(sections, "## thread_lifecycle_missing\nstatus: not_found")
	}
	return contextToolOutput("thread_context", symbol, sections), nil
}

func (p *ContextToolProvider) luaStateContext(ctx context.Context, args map[string]any) (string, error) {
	symbol := strings.TrimSpace(firstNonEmpty(stringArg(args, "symbol"), stringArg(args, "field"), stringArg(args, "function")))
	if symbol == "" {
		return "Error: symbol, field, or function is required", nil
	}
	patterns := scopePatterns(args)
	keywords := []string{symbol, "Init", "OnInit", "Register", "Unregister", "Callback", "Event", "State", "self." + symbol}
	var sections []string
	for _, keyword := range keywords {
		result, cost, err := p.Backend.Search(ctx, keyword, true, false, patterns, 10)
		if err != nil {
			return "", err
		}
		if hasMatches(result) {
			sections = append(sections, formatContextSection("lua_state_"+sanitizeSection(keyword), keyword, patterns, result, cost))
		}
	}
	if len(sections) == 0 {
		sections = append(sections, "## lua_state_context_missing\nstatus: not_found")
	}
	return contextToolOutput("lua_state_context", symbol, sections), nil
}

func stringArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func scopePatterns(args map[string]any) []string {
	var out []string
	for _, key := range []string{"scope", "path_hint", "file_path", "path"} {
		if s := strings.TrimSpace(stringArg(args, key)); s != "" {
			if key == "file_path" || key == "path" {
				out = append(out, dirScope(s))
			} else {
				out = append(out, s)
			}
		}
	}
	if raw, ok := args["file_patterns"].([]any); ok {
		for _, item := range raw {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
	}
	if raw, ok := args["scopes"].([]any); ok {
		for _, item := range raw {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func dirScope(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	if strings.HasSuffix(path, "/") {
		return path
	}
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	return path[:idx+1]
}

func cppHeaderCandidates(path string) []string {
	path = strings.ReplaceAll(path, "\\", "/")
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	var out []string
	for _, headerExt := range []string{".h", ".hpp", ".hh", ".hxx"} {
		out = append(out, base+headerExt)
	}
	return out
}

func hasMatches(result string) bool {
	trimmed := strings.TrimSpace(result)
	return trimmed != "" && !strings.HasPrefix(trimmed, "No matches found") && !strings.HasPrefix(trimmed, "Error:")
}

func formatContextSection(title, query string, patterns []string, result string, cost QueryCost) string {
	return fmt.Sprintf("## %s\nquery: %s\nscope: %s\nQUERY_COST: backend=%s p4_calls=%d scanned_files=%d elapsed_ms=%d truncated=%t\n%s",
		title, query, strings.Join(patterns, ","), cost.Backend, cost.P4Calls, cost.ScannedFiles, cost.ElapsedMS, cost.Truncated, strings.TrimSpace(result))
}

func contextToolOutput(toolName, subject string, sections []string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CONTEXT_TOOL: %s\nSUBJECT: %s\n", toolName, subject))
	if len(sections) == 0 {
		sb.WriteString("STATUS: missing_context\n")
		return sb.String()
	}
	sb.WriteString(strings.Join(sections, "\n\n"))
	return sb.String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func sanitizeSection(s string) string {
	s = strings.ToLower(s)
	replacer := strings.NewReplacer(".", "_", ":", "_", "/", "_", "\\", "_", " ", "_")
	return replacer.Replace(s)
}
