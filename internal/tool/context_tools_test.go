package tool

import (
	"context"
	"strings"
	"testing"
)

func TestContextTool_SymbolReferencesUsesBackendCost(t *testing.T) {
	p := NewContextTool(SymbolReferences, &FixtureQueryBackend{Files: map[string]string{
		"Game/Foo.lua": "local function RegisterRedPoint()\nRegisterRedPoint()\n",
	}})

	result, err := p.Execute(context.Background(), map[string]any{
		"symbol": "RegisterRedPoint",
		"scope":  "Game/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "CONTEXT_TOOL: symbol_references") {
		t.Fatalf("missing tool marker:\n%s", result)
	}
	if !strings.Contains(result, "File: Game/Foo.lua") || !strings.Contains(result, "QUERY_COST: backend=fixture") {
		t.Fatalf("expected fixture references and cost, got:\n%s", result)
	}
}

func TestContextTool_CppDeclContextReadsHeader(t *testing.T) {
	p := NewContextTool(CppDeclContext, &FixtureQueryBackend{Files: map[string]string{
		"Source/Foo.cpp": "void UFoo::Tick() {}\n",
		"Source/Foo.h":   "UCLASS()\nclass UFoo {\n  UFUNCTION()\n  void Tick();\n};\n",
	}})

	result, err := p.Execute(context.Background(), map[string]any{
		"path":   "Source/Foo.cpp",
		"symbol": "Tick",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "declaration_file_read") || !strings.Contains(result, "UFUNCTION()") {
		t.Fatalf("expected header context, got:\n%s", result)
	}
}

func TestContextTool_LuaStateContextFindsLifecycleTerms(t *testing.T) {
	p := NewContextTool(LuaStateContext, &FixtureQueryBackend{Files: map[string]string{
		"Game/Feature/View.lua": "function M:OnInit()\n self.State = 1\n self:RegisterEvent()\nend\n",
	}})

	result, err := p.Execute(context.Background(), map[string]any{
		"symbol": "State",
		"scope":  "Game/Feature/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "lua_state_state") || !strings.Contains(result, "RegisterEvent") {
		t.Fatalf("expected lua state context, got:\n%s", result)
	}
}

func TestContextTool_ThreadContextReportsMissing(t *testing.T) {
	p := NewContextTool(ThreadContext, &FixtureQueryBackend{Files: map[string]string{
		"Game/Feature/View.lua": "function M:Render()\nend\n",
	}})

	result, err := p.Execute(context.Background(), map[string]any{
		"symbol": "Render",
		"scope":  "Game/Feature/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "thread_lifecycle_missing") {
		t.Fatalf("expected missing lifecycle marker, got:\n%s", result)
	}
}

func TestContextTool_LocalIndexLuaStateContext(t *testing.T) {
	dir := t.TempDir()
	writeLocalIndexFile(t, dir, "files.jsonl", strings.Join([]string{
		`{"path":"Game/Feature/View.lua","content":"function M:OnInit()\n self.State = 1\n self:RegisterEvent()\nend\n"}`,
	}, "\n"))
	writeLocalIndexFile(t, dir, "grep_index.jsonl", strings.Join([]string{
		`{"path":"Game/Feature/View.lua","line":1,"text":"function M:OnInit()"}`,
		`{"path":"Game/Feature/View.lua","line":2,"text":" self.State = 1"}`,
		`{"path":"Game/Feature/View.lua","line":3,"text":" self:RegisterEvent()"}`,
		`{"path":"Game/Feature/View.lua","line":4,"text":"end"}`,
	}, "\n"))

	backend, err := NewLocalIndexQueryBackend(dir, []string{"Game/Feature/"})
	if err != nil {
		t.Fatal(err)
	}
	p := NewContextTool(LuaStateContext, backend)
	result, err := p.Execute(context.Background(), map[string]any{
		"symbol": "State",
		"scope":  "Game/Feature/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "QUERY_COST: backend=local-index p4_calls=0") || !strings.Contains(result, "self.State") {
		t.Fatalf("expected local-index cost and lua state evidence, got:\n%s", result)
	}
}
