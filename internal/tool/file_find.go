package tool

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

const (
	fileFindMaxCount = 100
	fileFindTimeout  = 10 * time.Second
)

// FileFindProvider finds files by name or pattern in the repository using git ls-files.
type FileFindProvider struct {
	FileReader *FileReader
	Backend    QueryBackend
}

func NewFileFind(fr *FileReader) *FileFindProvider {
	return &FileFindProvider{FileReader: fr, Backend: NewGitQueryBackend(fr)}
}

func (p *FileFindProvider) Tool() Tool { return FileFind }

func (p *FileFindProvider) Execute(ctx context.Context, args map[string]any) (string, error) {
	queryName, _ := args["query_name"].(string)
	if strings.TrimSpace(queryName) == "" {
		return "// The file was not found", nil
	}

	caseSensitive, _ := args["case_sensitive"].(bool)

	files, err := p.findFiles(ctx, queryName, caseSensitive)
	if err != nil {
		return "", err
	}

	if len(files) == 0 {
		return "// The file was not found", nil
	}
	return strings.Join(files, "\n"), nil
}

func (p *FileFindProvider) findFiles(ctx context.Context, queryName string, caseSensitive bool) ([]string, error) {
	backend := p.Backend
	if backend == nil {
		backend = NewGitQueryBackend(p.FileReader)
	}
	files, _, err := backend.FindFiles(ctx, queryName, caseSensitive, fileFindMaxCount)
	return files, err
}

// listGitFiles returns tracked and untracked files (respecting .gitignore) via git ls-files.
// In range/commit mode it uses git ls-tree to list files at the reviewed ref.
func (p *FileFindProvider) listGitFiles(parentCtx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(parentCtx, fileFindTimeout)
	defer cancel()

	var output []byte
	var err error

	var args []string
	if ref := p.FileReader.Ref; ref != "" {
		args = []string{"ls-tree", "-r", "--name-only", "--end-of-options", ref}
	} else {
		args = []string{"ls-files", "--cached", "--others", "--exclude-standard"}
	}

	if p.FileReader.Runner != nil {
		output, err = p.FileReader.Runner.Output(ctx, p.FileReader.RepoDir, args...)
	} else {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = p.FileReader.RepoDir
		output, err = cmd.Output()
	}

	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}

	var files []string
	lines := bytes.Split(bytes.TrimRight(output, "\n"), []byte{'\n'})
	for _, line := range lines {
		if len(line) > 0 {
			s := string(line)
			// Skip binary-like files that lack meaningful extensions patterns
			// and filter out paths in common generated/artifact directories.
			if shouldSkipFile(s) {
				continue
			}
			files = append(files, s)
		}
	}
	return files, nil
}

// shouldSkipFile returns true if a git ls-files output path should be skipped.
// Keeps only widely useful files (those with recognizable extensions).
func shouldSkipFile(path string) bool {
	// Keep extensionless build/config files like Makefile, Dockerfile, LICENSE
	base := path
	if idx := strings.LastIndex(path, "/"); idx != -1 {
		base = path[idx+1:]
	}
	hasExt := strings.Contains(base, ".")
	if !hasExt {
		// Allow well-known extensionless files
		switch base {
		case "Makefile", "Dockerfile", "LICENSE", "Vagrantfile", "Containerfile":
			return false
		}
		return true // skip other extensionless files
	}
	return false
}
