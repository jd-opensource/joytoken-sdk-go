package tooldef

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileSandbox confines file tools to a single root directory. Every path the
// model supplies is resolved relative to Root and validated to stay inside it,
// so the model can never read or write outside the sandbox. MaxBytes caps the
// size of a single read or write; zero means DefaultFileMaxBytes.
//
// Root may be left empty. In that case the sandbox falls back to a sensible
// wide boundary — the current user's home directory — so a host can offer a
// "just find my file" experience without pinning an exact path up front. This
// implements the recommended "wide boundary" model (tier B): the model is free
// to explore inside one broad root, while side-effecting operations still go
// through the permission gate with the resolved absolute path shown to the user.
//
// The implementation lives in the tooldef package (the bottom of the dependency
// graph) so the root client can reuse these tools for default fallback, and the
// agent/toolkit package re-exports them via thin delegates for a single source
// of truth.
type FileSandbox struct {
	Root     string
	MaxBytes int64
}

// DefaultFileMaxBytes is the per-operation size cap used when MaxBytes is zero.
const DefaultFileMaxBytes int64 = 1 << 20 // 1 MiB

// DefaultSearchLimit caps how many matches an exploration tool returns in one
// call, so a broad query over a large tree cannot flood the model's context.
const DefaultSearchLimit = 200

// FileRead returns a local, read-only tool that reads a UTF-8 text file from
// within the sandbox. It is side-effect free and safe under PermissionAuto.
func FileRead(sandbox FileSandbox) Tool {
	return Tool{
		Name:        "file_read",
		Description: "Read a UTF-8 text file located inside the configured directory. The path must be relative to that directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path relative to the sandbox root, e.g. \"notes/todo.txt\".",
				},
			},
			"required": []string{"path"},
		},
		Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) {
			rel, err := stringArg(input, "path")
			if err != nil {
				return nil, err
			}
			abs, err := sandbox.resolve(rel)
			if err != nil {
				return nil, fmt.Errorf("file_read: %w", err)
			}
			info, err := os.Stat(abs)
			if err != nil {
				return nil, fmt.Errorf("file_read: %w", err)
			}
			if info.IsDir() {
				return nil, fmt.Errorf("file_read: %q is a directory", rel)
			}
			max := sandbox.maxBytes()
			if info.Size() > max {
				return nil, fmt.Errorf("file_read: file is %d bytes, exceeds limit of %d", info.Size(), max)
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return nil, fmt.Errorf("file_read: %w", err)
			}
			return map[string]any{
				"path":    rel,
				"content": string(data),
				"bytes":   len(data),
			}, nil
		},
	}
}

// FileWrite returns a local tool that writes a UTF-8 text file inside the
// sandbox, creating parent directories as needed. Because it has real side
// effects, register it under PermissionAsk so the host approves each write.
func FileWrite(sandbox FileSandbox) Tool {
	return Tool{
		Name:        "file_write",
		Description: "Write a UTF-8 text file inside the configured directory, creating parent folders as needed. The path must be relative to that directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path relative to the sandbox root, e.g. \"out/report.md\".",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "The full UTF-8 text content to write. Existing files are overwritten.",
				},
			},
			"required": []string{"path", "content"},
		},
		Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) {
			rel, err := stringArg(input, "path")
			if err != nil {
				return nil, err
			}
			content, err := stringArg(input, "content")
			if err != nil {
				return nil, err
			}
			if max := sandbox.maxBytes(); int64(len(content)) > max {
				return nil, fmt.Errorf("file_write: content is %d bytes, exceeds limit of %d", len(content), max)
			}
			abs, err := sandbox.resolve(rel)
			if err != nil {
				return nil, fmt.Errorf("file_write: %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return nil, fmt.Errorf("file_write: %w", err)
			}
			if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
				return nil, fmt.Errorf("file_write: %w", err)
			}
			return map[string]any{
				"path":  rel,
				"bytes": len(content),
			}, nil
		},
	}
}

// ListDir returns a local, read-only tool that lists the immediate entries of a
// directory inside the sandbox. It is side-effect free and safe under
// PermissionAuto. It lets the model explore ("what's on the Desktop?") before
// it knows an exact path, which is what powers a "help me find a file" flow.
// It returns at most DefaultSearchLimit entries and sets "truncated" to true
// when the directory holds more, keeping the tool payload bounded.
func ListDir(sandbox FileSandbox) Tool {
	return Tool{
		Name:        "list_dir",
		Description: "List the immediate files and folders inside a directory located within the configured workspace. Use it to explore before you know an exact path. The path is relative to the workspace root; use \".\" for the root itself.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Directory path relative to the workspace root, e.g. \".\" or \"Desktop\".",
				},
			},
			"required": []string{"path"},
		},
		Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) {
			rel := optionalStringArg(input, "path")
			if rel == "" {
				rel = "."
			}
			abs, err := sandbox.resolve(rel)
			if err != nil {
				return nil, fmt.Errorf("list_dir: %w", err)
			}
			entries, err := os.ReadDir(abs)
			if err != nil {
				return nil, fmt.Errorf("list_dir: %w", err)
			}
			items := make([]map[string]any, 0, len(entries))
			truncated := false
			for _, e := range entries {
				if len(items) >= DefaultSearchLimit {
					truncated = true
					break
				}
				item := map[string]any{
					"name": e.Name(),
					"dir":  e.IsDir(),
				}
				if info, ierr := e.Info(); ierr == nil && !e.IsDir() {
					item["bytes"] = info.Size()
				}
				items = append(items, item)
			}
			return map[string]any{
				"path":      rel,
				"entries":   items,
				"truncated": truncated,
			}, nil
		},
	}
}

// FileSearch returns a local, read-only tool that recursively finds files whose
// name matches a glob pattern anywhere under the sandbox root. It is the tool a
// model reaches for when the user says "find my file on the Desktop" without
// knowing the exact path. It is side-effect free and safe under PermissionAuto.
func FileSearch(sandbox FileSandbox) Tool {
	return Tool{
		Name:        "file_search",
		Description: "Recursively search the configured workspace for files whose name matches a glob pattern (e.g. \"*.pdf\", \"report*\"). Returns matching paths relative to the workspace root. Use it to locate a file when you do not know its exact path.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern matched against each file's base name, e.g. \"*.txt\" or \"invoice*\".",
				},
			},
			"required": []string{"pattern"},
		},
		Execute: func(_ context.Context, input any, _ ToolExecutionContext) (any, error) {
			pattern, err := stringArg(input, "pattern")
			if err != nil {
				return nil, err
			}
			root, err := sandbox.rootDir()
			if err != nil {
				return nil, fmt.Errorf("file_search: %w", err)
			}
			matches := make([]string, 0, 16)
			truncated := false
			walkErr := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					if d != nil && d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if d.IsDir() {
					return nil
				}
				ok, merr := filepath.Match(pattern, d.Name())
				if merr != nil {
					return merr
				}
				if !ok {
					return nil
				}
				rel, rerr := filepath.Rel(root, p)
				if rerr != nil {
					return nil
				}
				matches = append(matches, rel)
				if len(matches) >= DefaultSearchLimit {
					truncated = true
					return filepath.SkipAll
				}
				return nil
			})
			if walkErr != nil {
				return nil, fmt.Errorf("file_search: %w", walkErr)
			}
			sort.Strings(matches)
			return map[string]any{
				"pattern":   pattern,
				"matches":   matches,
				"count":     len(matches),
				"truncated": truncated,
			}, nil
		},
	}
}

// AbsRoot returns the effective sandbox root as an absolute path. When Root is
// empty it falls back to the current user's home directory, giving hosts a
// zero-config wide boundary. It is exported so callers (e.g. the root client's
// permission prompt) can show the user exactly which directory a tool operates
// in before approving a side-effecting call.
func (s FileSandbox) AbsRoot() (string, error) {
	root := s.Root
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("sandbox root is empty and home directory is unavailable: %w", err)
		}
		root = home
	}
	return filepath.Abs(root)
}

// rootDir is the internal accessor for the resolved absolute root.
func (s FileSandbox) rootDir() (string, error) { return s.AbsRoot() }

// resolve turns a model-supplied relative path into an absolute path and
// guarantees it stays within the sandbox root, defeating "../" traversal,
// absolute-path escapes, and symlink escapes. Lexical containment alone is not
// enough: a symlink inside the sandbox can point outside it, so we resolve the
// real (symlink-free) path of both the root and the deepest existing ancestor
// of the target before comparing. The target itself may not exist yet (e.g. a
// fresh file_write), so we only evaluate symlinks up to the closest existing
// parent and treat the not-yet-created leaf segments lexically.
func (s FileSandbox) resolve(rel string) (string, error) {
	root, err := s.rootDir()
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path must be relative, got absolute path %q", rel)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("cannot resolve sandbox root: %w", err)
	}
	abs := filepath.Join(root, filepath.Clean("/"+rel))
	// Reject on the lexical form first (cheap, catches the common cases).
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the sandbox root", rel)
	}
	// Then verify the real path of the deepest existing ancestor is still
	// contained, so an intermediate symlink cannot hop outside realRoot.
	real, err := evalDeepestExisting(abs)
	if err != nil {
		return "", err
	}
	if real != realRoot && !strings.HasPrefix(real, realRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the sandbox root via a symlink", rel)
	}
	return abs, nil
}

// evalDeepestExisting resolves symlinks on the longest existing prefix of abs
// and re-appends the remaining (not-yet-created) segments lexically. This lets
// resolve validate write targets that do not exist yet without being fooled by
// a symlinked parent directory.
func evalDeepestExisting(abs string) (string, error) {
	remaining := ""
	current := abs
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			if remaining == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, remaining), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("cannot resolve path: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Reached the filesystem root without finding an existing prefix.
			return abs, nil
		}
		remaining = filepath.Join(filepath.Base(current), remaining)
		current = parent
	}
}

func (s FileSandbox) maxBytes() int64 {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return DefaultFileMaxBytes
}
