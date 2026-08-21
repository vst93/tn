package app

// Git-based note synchronization.
//
// The notebook directory doubles as a Git working tree: TN initializes it on
// first sync, commits every change and pushes to a user-configured remote.
// Pulls use --rebase so local and remote history stay linear, which is what a
// single-user notes repository wants. Credentials are delegated entirely to
// the user's Git setup (ssh-agent, credential helper); TN never stores them.
//
// The same history powers per-note version browsing: git log lists the
// revisions of one file and git show materializes any of them for preview or
// rollback.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// GitSyncConfig holds the user-configurable sync settings. It lives next to
// the notes as a dotfile so it travels with clones but stays out of renders.
type GitSyncConfig struct {
	Remote   string `json:"remote"`    // repository URL; empty = local-only commits
	Branch   string `json:"branch"`    // defaults to "main"
	Author   string `json:"author"`    // "Name <email>" for commit identity
	AutoSync bool   `json:"auto_sync"` // periodic push in the background
	AutoMins int    `json:"auto_sync_minutes"`
}

func gitSyncConfigPath(root string) string {
	return filepath.Join(root, ".git-sync.json")
}

func loadGitSyncConfig(root string) GitSyncConfig {
	config := GitSyncConfig{Branch: "main", AutoMins: 10}
	data, err := os.ReadFile(gitSyncConfigPath(root))
	if err != nil {
		return config
	}
	_ = json.Unmarshal(data, &config)
	if config.Branch == "" {
		config.Branch = "main"
	}
	return config
}

func saveGitSyncConfig(root string, config GitSyncConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(gitSyncConfigPath(root), data, 0o600)
}

// gitRun executes one git command inside the notebook root.
func gitRun(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

func gitInstalled() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// gitEnsureRepo makes sure root is a working tree with the configured branch
// and remote. Safe to call before every operation.
func gitEnsureRepo(root string, config GitSyncConfig) error {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		if _, err := gitRun(root, "init"); err != nil {
			return err
		}
	}
	if _, err := gitRun(root, "rev-parse", "--verify", config.Branch); err != nil {
		// Branch missing locally: create it from the current HEAD (or orphan).
		if _, err := gitRun(root, "checkout", "-B", config.Branch); err != nil {
			return err
		}
	} else if _, err := gitRun(root, "checkout", config.Branch); err != nil {
		return err
	}
	if config.Remote != "" {
		if _, err := gitRun(root, "remote", "get-url", "origin"); err != nil {
			if _, err := gitRun(root, "remote", "add", "origin", config.Remote); err != nil {
				return err
			}
		} else if _, err := gitRun(root, "remote", "set-url", "origin", config.Remote); err != nil {
			return err
		}
	}
	return nil
}

// gitCommitIdentity returns -c overrides for authorship so a global git
// identity is not required.
func gitCommitIdentity(config GitSyncConfig) []string {
	name, email := "tn", "tn@local"
	if config.Author != "" {
		if name2, email2, ok := strings.Cut(config.Author, "<"); ok {
			name = strings.TrimSpace(name2)
			email = strings.TrimSuffix(strings.TrimSpace(email2), ">")
		} else {
			name = config.Author
		}
	}
	return []string{"-c", "user.name=" + name, "-c", "user.email=" + email}
}

// gitPush stages everything, commits and pushes to the configured remote.
// Returns a human-readable summary either way.
func gitPush(root string, config GitSyncConfig) (string, error) {
	if !gitInstalled() {
		return "", fmt.Errorf("git 未安装或不在 PATH 中")
	}
	if err := gitEnsureRepo(root, config); err != nil {
		return "", err
	}
	if _, err := gitRun(root, "add", "-A"); err != nil {
		return "", err
	}
	identity := gitCommitIdentity(config)
	commitArgs := append([]string{}, identity...)
	commitArgs = append(commitArgs, "commit", "-m", "tn sync "+time.Now().Format("2006-01-02 15:04"))
	out, err := gitRun(root, commitArgs...)
	hasLocal := true
	if err != nil {
		if !isNothingToCommit(err, out) {
			return "", err
		}
		hasLocal = false
	}
	if config.Remote == "" {
		if hasLocal {
			return "已提交到本地仓库（未配置远程）", nil
		}
		return "没有需要同步的更改", nil
	}
	if hasLocal {
		// Rebase on top of the remote first so pushes do not bounce.
		if _, err := gitRun(root, "pull", "--rebase", "origin", config.Branch); err != nil {
			return "", fmt.Errorf("拉取远程更改失败（可能存在冲突，请手动解决后重试）: %v", err)
		}
	}
	if _, err := gitRun(root, "push", "-u", "origin", config.Branch); err != nil {
		return "", err
	}
	if hasLocal {
		return "✓ 已提交并推送到 " + config.Branch, nil
	}
	return "✓ 已推送到 " + config.Branch, nil
}

func isNothingToCommit(err error, out string) bool {
	combined := strings.ToLower(err.Error() + " " + out)
	return strings.Contains(combined, "nothing to commit") || strings.Contains(combined, "no changes added")
}

// gitPull rebases local commits on top of the remote branch.
func gitPull(root string, config GitSyncConfig) (string, error) {
	if !gitInstalled() {
		return "", fmt.Errorf("git 未安装或不在 PATH 中")
	}
	if config.Remote == "" {
		return "", fmt.Errorf("未配置远程仓库地址")
	}
	if err := gitEnsureRepo(root, config); err != nil {
		return "", err
	}
	if _, err := gitRun(root, "fetch", "origin"); err != nil {
		return "", err
	}
	out, err := gitRun(root, "pull", "--rebase", "origin", config.Branch)
	if err != nil {
		return "", fmt.Errorf("拉取失败（可能存在冲突，请手动解决后重试）: %v", err)
	}
	if strings.Contains(out, "Already up to date") || strings.Contains(out, "已是最新") {
		return "已是最新", nil
	}
	// Count incoming commits roughly.
	n := strings.Count(out, "\n")
	return fmt.Sprintf("✓ 已拉取远程更新（约 %d 行变更）", n), nil
}

// GitVersion is one historical revision of a single note.
type GitVersion struct {
	Hash    string
	Date    string
	Subject string
}

// gitNoteHistory lists the revisions touching one note, newest first.
func gitNoteHistory(root, relPath string) ([]GitVersion, error) {
	if !gitInstalled() {
		return nil, fmt.Errorf("git 未安装或不在 PATH 中")
	}
	out, err := gitRun(root, "log", "--date=format:%Y-%m-%d %H:%M", "--format=%H%x09%ad%x09%s", "--", relPath)
	if err != nil {
		// A repo without any commit yet, or a file never committed, simply has
		// no history.
		return nil, nil
	}
	var versions []GitVersion
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		v := GitVersion{Hash: parts[0]}
		if len(parts) > 1 {
			v.Date = parts[1]
		}
		if len(parts) > 2 {
			v.Subject = parts[2]
		}
		versions = append(versions, v)
	}
	return versions, nil
}

// gitShowFile materializes a note's content at a given revision.
func gitShowFile(root, hash, relPath string) (string, error) {
	return gitRun(root, "show", hash+":"+relPath)
}

// gitResultMsg carries the outcome of an async push/pull back to the UI.
type gitResultMsg struct {
	action string // "推送” / "拉取"
	detail string
	err    error
}

// gitActionCmd runs a sync operation off the UI thread.
func gitActionCmd(action string, fn func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		detail, err := fn()
		return gitResultMsg{action: action, detail: detail, err: err}
	}
}

// gitSyncTickMsg fires periodically; the handler checks whether auto-sync is
// due and pushes in the background.
type gitSyncTickMsg struct{}

func gitSyncTickCmd() tea.Cmd {
	return tea.Tick(time.Minute, func(time.Time) tea.Msg { return gitSyncTickMsg{} })
}
