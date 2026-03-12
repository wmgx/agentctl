package claude

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	markerStart      = "<<CC_START>>"
	markerEndPrefix  = "<<CC_END:"
	markerEndSuffix  = ">>"
	tailPollInterval = 100 * time.Millisecond
	cleanupInterval  = 5 * time.Minute
	idleTimeout      = time.Hour
)

var ansiRe = regexp.MustCompile(`\x1b\[\??[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[=>]|\r|[\x00-\x08\x0e-\x1f]`)

// tmuxSession represents a managed tmux session.
type tmuxSession struct {
	name string
}

// TmuxRunner manages tmux sessions for running Claude CLI.
type TmuxRunner struct {
	dir        string
	activeMu   sync.Mutex
	active     map[string]time.Time
	sessionsMu sync.RWMutex
	sessions   map[string]*tmuxSession
	stopCh     chan struct{}
}

// NewTmuxRunner creates a TmuxRunner with the given data directory.
func NewTmuxRunner(dataDir string) *TmuxRunner {
	dir := filepath.Join(dataDir, "tmux")
	os.MkdirAll(dir, 0755)
	r := &TmuxRunner{
		dir:      dir,
		active:   make(map[string]time.Time),
		sessions: make(map[string]*tmuxSession),
		stopCh:   make(chan struct{}),
	}
	go r.cleanupLoop()
	return r
}

// Stop kills all managed tmux sessions and stops the cleanup loop.
func (r *TmuxRunner) Stop() {
	close(r.stopCh)
	r.activeMu.Lock()
	for name := range r.active {
		tmuxKill(name)
	}
	r.activeMu.Unlock()
}

func (r *TmuxRunner) trackSession(name string) {
	r.activeMu.Lock()
	r.active[name] = time.Now()
	r.activeMu.Unlock()
}

func (r *TmuxRunner) untrackSession(name string) {
	r.activeMu.Lock()
	delete(r.active, name)
	r.activeMu.Unlock()
}

func (r *TmuxRunner) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.activeMu.Lock()
			now := time.Now()
			for name, lastActive := range r.active {
				if now.Sub(lastActive) > idleTimeout {
					log.Printf("[tmux] idle timeout, killing session: %s", name)
					tmuxKill(name)
					delete(r.active, name)
					r.cleanFiles(name)
				}
			}
			r.activeMu.Unlock()
		}
	}
}

func (r *TmuxRunner) cleanFiles(name string) {
	os.Remove(filepath.Join(r.dir, name+".log"))
	os.Remove(filepath.Join(r.dir, name+".sh"))
	os.Remove(filepath.Join(r.dir, name+".prompt"))
}

// ExecCollect runs a command in tmux and collects all output between markers.
// Used by RunOnce for single-turn text output.
func (r *TmuxRunner) ExecCollect(ctx context.Context, env map[string]string, cliPath, prompt string, args []string, cwd string) (string, error) {
	name := "cc-once-" + uuid.New().String()[:8]
	outputFile := filepath.Join(r.dir, name+".log")
	promptFile := filepath.Join(r.dir, name+".prompt")
	scriptFile := filepath.Join(r.dir, name+".sh")

	defer func() {
		tmuxKill(name)
		r.untrackSession(name)
		os.Remove(outputFile)
		os.Remove(promptFile)
		os.Remove(scriptFile)
	}()

	if err := os.WriteFile(promptFile, []byte(prompt), 0644); err != nil {
		return "", fmt.Errorf("write prompt: %w", err)
	}

	// Script redirects output directly to outputFile, bypassing pipe-pane
	// This avoids ANSI escape sequences and terminal buffering issues.
	script := buildScript(env, cliPath, promptFile, outputFile, cwd, args)
	if err := os.WriteFile(scriptFile, []byte(script), 0755); err != nil {
		return "", fmt.Errorf("write script: %w", err)
	}

	if err := os.WriteFile(outputFile, nil, 0644); err != nil {
		return "", fmt.Errorf("create output: %w", err)
	}

	if err := tmuxNew(name); err != nil {
		return "", fmt.Errorf("tmux new-session: %w", err)
	}
	r.trackSession(name)

	if err := tmuxSend(name, fmt.Sprintf("bash '%s'", shellEscape(scriptFile))); err != nil {
		return "", fmt.Errorf("tmux send-keys: %w", err)
	}
	log.Printf("[tmux] ExecCollect started: session=%s output=%s", name, outputFile)

	var result strings.Builder
	started := false

	err := r.tailUntilDone(ctx, name, outputFile, func(line string) {
		if strings.Contains(line, markerStart) {
			started = true
			return
		}
		if strings.Contains(line, markerEndPrefix) {
			return
		}
		if started {
			log.Printf("[tmux] collect line: noise=%v %q", isNoiseLine(line), line)
			if !isNoiseLine(line) {
				if result.Len() > 0 {
					result.WriteString("\n")
				}
				result.WriteString(line)
			}
		}
	})
	if err != nil {
		return "", err
	}

	text := result.String()
	log.Printf("[tmux] ExecCollect result len=%d", len(text))
	return text, nil
}

// ExecStream runs a command in tmux and streams output lines between markers to handler.
// Used by Run for streaming stream-json output.
func (r *TmuxRunner) ExecStream(ctx context.Context, env map[string]string, cliPath, prompt string, args []string, cwd string, handler func(line string)) error {
	name := "cc-stream-" + uuid.New().String()[:8]
	outputFile := filepath.Join(r.dir, name+".log")
	promptFile := filepath.Join(r.dir, name+".prompt")
	scriptFile := filepath.Join(r.dir, name+".sh")

	defer func() {
		tmuxKill(name)
		r.untrackSession(name)
		os.Remove(outputFile)
		os.Remove(promptFile)
		os.Remove(scriptFile)
	}()

	if err := os.WriteFile(promptFile, []byte(prompt), 0644); err != nil {
		return fmt.Errorf("write prompt: %w", err)
	}

	// Script redirects output directly to outputFile, bypassing pipe-pane.
	script := buildScript(env, cliPath, promptFile, outputFile, cwd, args)
	if err := os.WriteFile(scriptFile, []byte(script), 0755); err != nil {
		return fmt.Errorf("write script: %w", err)
	}

	if err := os.WriteFile(outputFile, nil, 0644); err != nil {
		return fmt.Errorf("create output: %w", err)
	}

	if err := tmuxNew(name); err != nil {
		return fmt.Errorf("tmux new-session: %w", err)
	}
	r.trackSession(name)

	if err := tmuxSend(name, fmt.Sprintf("bash '%s'", shellEscape(scriptFile))); err != nil {
		return fmt.Errorf("tmux send-keys: %w", err)
	}
	log.Printf("[tmux] ExecStream started: session=%s output=%s", name, outputFile)

	started := false
	err := r.tailUntilDone(ctx, name, outputFile, func(line string) {
		if strings.Contains(line, markerStart) {
			started = true
			return
		}
		if strings.Contains(line, markerEndPrefix) {
			return
		}
		if started {
			handler(line)
		}
	})
	if err != nil {
		// 保留输出文件方便排查，记录路径
		if content, rerr := os.ReadFile(outputFile); rerr == nil {
			log.Printf("[tmux] ExecStream failed, output:\n%s", string(content))
		}
	}
	return err
}

// tailUntilDone polls the output file until the end marker is found, context is cancelled,
// or the tmux session dies unexpectedly.
func (r *TmuxRunner) tailUntilDone(ctx context.Context, sessionName, path string, handler func(string)) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open output: %w", err)
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	idlePolls := 0
	const sessionCheckInterval = 50 // check every 50 polls (~5s at 100ms)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			idlePolls++
			// Periodically check if tmux session is still alive
			if idlePolls%sessionCheckInterval == 0 {
				if !tmuxHasSession(sessionName) {
					return fmt.Errorf("tmux session %s exited unexpectedly", sessionName)
				}
			}
			time.Sleep(tailPollInterval)
			continue
		}

		idlePolls = 0
		line = strings.TrimRight(line, "\n\r")
		if line == "" {
			continue
		}

		clean := stripANSI(line)
		if strings.Contains(clean, markerEndPrefix) {
			log.Printf("[tmux] end marker detected: %q", clean)
			if strings.Contains(clean, markerEndSuffix) {
				code := strings.TrimSuffix(strings.TrimPrefix(clean, markerEndPrefix), markerEndSuffix)
				code = strings.TrimSpace(code)
				if code != "0" {
					return fmt.Errorf("claude cli exited with code %s", code)
				}
				return nil
			}
		}
		handler(clean)
	}
}

// buildScript generates a bash script to run Claude CLI with the given parameters.
// Output is redirected directly to outputFile to avoid pipe-pane buffering/ANSI issues.
// cwd is the working directory for the claude command; empty means current directory.
func buildScript(env map[string]string, cliPath, promptFile, outputFile, cwd string, extraArgs []string) string {
	var sb strings.Builder
	sb.WriteString("#!/bin/bash\n")
	sb.WriteString("export LANG=en_US.UTF-8\n")
	sb.WriteString("export LC_ALL=en_US.UTF-8\n")
	sb.WriteString("unset CLAUDECODE\n")
	if cwd != "" {
		sb.WriteString(fmt.Sprintf("cd '%s'\n", shellEscape(cwd)))
	}
	for k, v := range env {
		sb.WriteString(fmt.Sprintf("export %s='%s'\n", k, shellEscape(v)))
	}
	sb.WriteString(fmt.Sprintf("_CC_PROMPT=$(cat '%s')\n", shellEscape(promptFile)))
	// Write markers and claude output directly to file; no pipe-pane needed.
	outArg := shellEscape(outputFile)
	sb.WriteString(fmt.Sprintf("echo '%s' >> '%s'\n", markerStart, outArg))
	sb.WriteString(fmt.Sprintf("'%s' -p \"$_CC_PROMPT\"", shellEscape(cliPath)))
	for _, arg := range extraArgs {
		sb.WriteString(fmt.Sprintf(" '%s'", shellEscape(arg)))
	}
	sb.WriteString(fmt.Sprintf(" >> '%s' 2>&1\n", outArg))
	sb.WriteString(fmt.Sprintf("echo '%s'\"$?\"'%s' >> '%s'\n", markerEndPrefix, markerEndSuffix, outArg))
	return sb.String()
}

// shellEscape escapes single quotes for use in bash single-quoted strings.
func shellEscape(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}

// stripANSI removes ANSI escape codes from a string.
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// tmux helpers

func tmuxNew(name string) error {
	_, err := tmuxExec("new-session", "-d", "-s", name, "-x", "200", "-y", "50")
	return err
}

func tmuxKill(name string) {
	tmuxExec("kill-session", "-t", name)
}

func tmuxHasSession(name string) bool {
	_, err := tmuxExec("has-session", "-t", name)
	return err == nil
}

func tmuxSend(name, keys string) error {
	_, err := tmuxExec("send-keys", "-t", name, keys, "Enter")
	return err
}

func tmuxExec(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %s (%w)", args[0], strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// isNoiseLine filters out non-content lines from tmux pipe-pane output.
func isNoiseLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	// Filter claude CLI hook errors and shell noise
	if strings.Contains(line, "hook") && strings.Contains(line, "failed:") {
		return true
	}
	if strings.HasPrefix(trimmed, "SessionEnd") || strings.HasPrefix(trimmed, "SessionStart") {
		return true
	}
	return false
}

// SendKeys 向指定 tmux session 发送按键输入
// 使用 -l (literal) 模式避免特殊字符转义问题
func (r *TmuxRunner) SendKeys(sessionID, text string) error {
	r.sessionsMu.RLock()
	sess, exists := r.sessions[sessionID]
	r.sessionsMu.RUnlock()

	if !exists {
		return fmt.Errorf("session %s not found", sessionID)
	}

	// 使用 -l (literal) 模式发送文本，避免特殊字符被解释
	cmd := exec.Command("tmux", "send-keys", "-l", "-t", sess.name, text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux send-keys -l failed: %w", err)
	}

	// 发送回车键
	cmd = exec.Command("tmux", "send-keys", "-t", sess.name, "Enter")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux send-keys Enter failed: %w", err)
	}

	return nil
}
