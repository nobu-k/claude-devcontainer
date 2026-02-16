package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

//go:embed Dockerfile
var dockerfile []byte

//go:embed .dockerignore
var dockerignore []byte

// exitCodeError wraps a non-zero exit code so defers run before the process exits.
type exitCodeError struct {
	code int
}

func (e exitCodeError) Error() string {
	return fmt.Sprintf("exit status %d", e.code)
}

func main() {
	var flagName string
	var flagVCS string

	rootCmd := &cobra.Command{
		Use:   "devcontainer [flags] [-- command...]",
		Short: "Launch a Claude devcontainer",
		Long:  "Creates a Docker container with Claude Code and development tools, using VCS worktrees for isolation.",
		// Accept arbitrary args after --
		DisableFlagParsing: false,
		Args:               cobra.ArbitraryArgs,
		SilenceUsage:       true,
		SilenceErrors:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(flagName, flagVCS, args)
		},
	}

	rootCmd.Flags().StringVar(&flagName, "name", "", "name for worktree/container (default: random suffix)")
	rootCmd.Flags().StringVar(&flagVCS, "vcs", "", "override VCS type: git or jj (default: auto-detect)")

	if err := rootCmd.Execute(); err != nil {
		var ec exitCodeError
		if errors.As(err, &ec) {
			os.Exit(ec.code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(name, vcsFlag string, extraArgs []string) error {
	containerName := envOrDefault("CONTAINER_NAME", "claude-dev")
	imageName := envOrDefault("IMAGE_NAME", "claude-devcontainer")

	workspaceDir := os.Getenv("BUILD_WORKSPACE_DIRECTORY")
	if workspaceDir == "" {
		var err error
		workspaceDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
	}

	// VCS resolution: flag > env > auto-detect
	vcs := vcsFlag
	if vcs == "" {
		vcs = os.Getenv("DEVCONTAINER_VCS")
	}
	if vcs == "" {
		if isDir(filepath.Join(workspaceDir, ".jj")) {
			vcs = "jj"
		} else if isDir(filepath.Join(workspaceDir, ".git")) {
			vcs = "git"
		}
	}
	if vcs != "" && vcs != "git" && vcs != "jj" {
		return fmt.Errorf("unknown VCS type: %s (expected 'git' or 'jj')", vcs)
	}

	var worktreeDir string
	var originalWorkspace string
	var branchName string   // git only
	var worktreeName string // jj only
	var suffix string

	if vcs != "" {
		if name != "" {
			suffix = name
			worktreeDir = filepath.Join(os.TempDir(), "devcontainer-"+name)
			// Remove existing directory if present
			os.RemoveAll(worktreeDir)
		} else {
			dir, err := os.MkdirTemp("", "devcontainer-")
			if err != nil {
				return fmt.Errorf("creating temp dir: %w", err)
			}
			// Remove it — VCS will recreate
			os.Remove(dir)
			suffix = filepath.Base(dir)
			// suffix already starts with "devcontainer-", strip it for the name
			suffix = strings.TrimPrefix(suffix, "devcontainer-")
			worktreeDir = dir
		}

		containerName = "devcontainer-" + suffix

		switch vcs {
		case "git":
			branchName = "devcontainer-" + suffix
			if err := runCmd("git", "-C", workspaceDir, "worktree", "add", "-b", branchName, worktreeDir); err != nil {
				return fmt.Errorf("creating git worktree: %w", err)
			}
		case "jj":
			worktreeName = "devcontainer-" + suffix
			if err := runCmd("jj", "-R", workspaceDir, "workspace", "add", "--name", worktreeName, worktreeDir); err != nil {
				return fmt.Errorf("creating jj workspace: %w", err)
			}
		}

		originalWorkspace = workspaceDir
		workspaceDir = worktreeDir
	}

	// Write embedded files to temp dir for docker build context
	contextDir, err := os.MkdirTemp("", "devcontainer-context-")
	if err != nil {
		return fmt.Errorf("creating context dir: %w", err)
	}
	defer os.RemoveAll(contextDir)

	if err := os.WriteFile(filepath.Join(contextDir, "Dockerfile"), dockerfile, 0644); err != nil {
		return fmt.Errorf("writing Dockerfile: %w", err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, ".dockerignore"), dockerignore, 0644); err != nil {
		return fmt.Errorf("writing .dockerignore: %w", err)
	}

	// Detect UID/GID
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("getting current user: %w", err)
	}
	hostUID := u.Uid
	hostGID := u.Gid

	// Docker socket GID
	dockerSock := "/var/run/docker.sock"
	dockerGID := "984" // fallback
	if info, err := os.Stat(dockerSock); err == nil {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			dockerGID = strconv.FormatUint(uint64(stat.Gid), 10)
		}
	}

	devHome := "/home/dev"
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}

	// Build image
	if err := runCmd("docker", "build",
		"--build-arg", "USER_UID="+hostUID,
		"--build-arg", "USER_GID="+hostGID,
		"--build-arg", "DOCKER_GID="+dockerGID,
		"-t", imageName,
		contextDir,
	); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}

	// Remove pre-existing container (suppress errors if it doesn't exist)
	rmCmd := exec.Command("docker", "rm", "-f", containerName)
	rmCmd.Stdout = nil
	rmCmd.Stderr = nil
	rmCmd.Run()

	// Trust /workspace in claude.json
	claudeJSON := filepath.Join(homeDir, ".claude.json")
	if err := trustWorkspace(claudeJSON); err != nil {
		// Non-fatal: warn and continue
		fmt.Fprintf(os.Stderr, "warning: could not update %s: %v\n", claudeJSON, err)
	}

	// Build mount and env arguments
	var mounts []string
	var envArgs []string

	addMount := func(src, dst string, ro bool) {
		opt := ""
		if ro {
			opt = ":ro"
		}
		mounts = append(mounts, "-v", src+":"+dst+opt)
	}

	addMount(workspaceDir, "/workspace", false)
	addMount(filepath.Join(homeDir, ".cache/bazelisk"), devHome+"/.cache/bazelisk", true)
	addMount(filepath.Join(homeDir, ".cargo"), devHome+"/.cargo", true)
	addMount(filepath.Join(homeDir, ".rustup"), devHome+"/.rustup", true)
	addMount(filepath.Join(homeDir, "go"), devHome+"/go", true)
	addMount(filepath.Join(homeDir, "dev/go"), devHome+"/gopath", false)
	addMount(filepath.Join(homeDir, ".npm"), devHome+"/.npm", true)
	addMount(filepath.Join(homeDir, ".cache/pnpm"), devHome+"/.cache/pnpm", true)
	addMount(filepath.Join(homeDir, ".claude"), devHome+"/.claude", false)
	addMount(filepath.Join(homeDir, ".claude.json"), devHome+"/.claude.json", false)

	// Bazel output base (only if repo uses Bazel)
	if fileExists(filepath.Join(workspaceDir, "MODULE.bazel")) {
		out, err := exec.Command("bazel", "info", "output_base").Output()
		if err == nil {
			outputBase := strings.TrimSpace(string(out))
			bazelRC, err := os.CreateTemp("", "bazel-rc-")
			if err == nil {
				fmt.Fprintf(bazelRC, "startup --output_base=%s\n", outputBase)
				bazelRC.Close()
				addMount(outputBase, outputBase, false)
				addMount(bazelRC.Name(), "/etc/bazel.bazelrc", true)
				defer os.Remove(bazelRC.Name())
			}
		}
	}

	// Docker socket
	if isSocket(dockerSock) {
		addMount(dockerSock, dockerSock, false)
	}

	// Conditional mounts
	if fileExists(filepath.Join(homeDir, ".gitconfig")) {
		addMount(filepath.Join(homeDir, ".gitconfig"), devHome+"/.gitconfig", true)
	}
	if isDir(filepath.Join(homeDir, ".config/jj")) {
		addMount(filepath.Join(homeDir, ".config/jj"), devHome+"/.config/jj", true)
	}
	if isDir(filepath.Join(homeDir, ".ssh")) {
		addMount(filepath.Join(homeDir, ".ssh"), devHome+"/.ssh", true)
	}

	// SSH agent forwarding
	if sshSock := os.Getenv("SSH_AUTH_SOCK"); sshSock != "" {
		addMount(sshSock, "/tmp/ssh-agent.sock", false)
		envArgs = append(envArgs, "-e", "SSH_AUTH_SOCK=/tmp/ssh-agent.sock")
	}

	// Worktree VCS backend: mount original repo's VCS dir
	if worktreeDir != "" {
		switch vcs {
		case "git":
			addMount(filepath.Join(originalWorkspace, ".git"), originalWorkspace+"/.git", false)
		case "jj":
			addMount(filepath.Join(originalWorkspace, ".jj/repo"), originalWorkspace+"/.jj/repo", false)
		}
	}

	// Build docker run args
	dockerArgs := []string{"run", "--rm", "-i", "--name", containerName}

	// Allocate TTY if stdin is a terminal
	if term.IsTerminal(int(os.Stdin.Fd())) {
		dockerArgs = append(dockerArgs, "-t")
	}

	dockerArgs = append(dockerArgs, mounts...)
	dockerArgs = append(dockerArgs, envArgs...)
	dockerArgs = append(dockerArgs, imageName)
	dockerArgs = append(dockerArgs, extraArgs...)

	// Run docker as subprocess with signal forwarding
	dockerCmd := exec.Command("docker", dockerArgs...)
	dockerCmd.Stdin = os.Stdin
	dockerCmd.Stdout = os.Stdout
	dockerCmd.Stderr = os.Stderr

	// Forward signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if err := dockerCmd.Start(); err != nil {
		cleanupWorktree(worktreeDir, vcs, originalWorkspace, branchName, worktreeName)
		return fmt.Errorf("starting docker: %w", err)
	}

	go func() {
		for sig := range sigCh {
			if dockerCmd.Process != nil {
				dockerCmd.Process.Signal(sig)
			}
		}
	}()

	exitCode := 0
	if err := dockerCmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			cleanupWorktree(worktreeDir, vcs, originalWorkspace, branchName, worktreeName)
			return fmt.Errorf("running docker: %w", err)
		}
	}

	signal.Stop(sigCh)
	close(sigCh)

	// Cleanup worktree
	cleanupWorktree(worktreeDir, vcs, originalWorkspace, branchName, worktreeName)

	if exitCode != 0 {
		return exitCodeError{code: exitCode}
	}
	return nil
}

func cleanupWorktree(worktreeDir, vcs, originalWorkspace, branchName, worktreeName string) {
	if worktreeDir == "" {
		return
	}
	switch vcs {
	case "git":
		runCmd("git", "-C", originalWorkspace, "worktree", "remove", "--force", worktreeDir)
		// Delete branch only if fully merged
		if err := runCmd("git", "-C", originalWorkspace, "merge-base", "--is-ancestor", branchName, "HEAD"); err == nil {
			runCmd("git", "-C", originalWorkspace, "branch", "-d", branchName)
		}
	case "jj":
		runCmd("jj", "-R", originalWorkspace, "workspace", "forget", worktreeName)
		os.RemoveAll(worktreeDir)
	}
}

func trustWorkspace(claudeJSONPath string) error {
	if !fileExists(claudeJSONPath) {
		return nil
	}

	data, err := os.ReadFile(claudeJSONPath)
	if err != nil {
		return err
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}

	projects, ok := config["projects"].(map[string]interface{})
	if !ok {
		projects = make(map[string]interface{})
		config["projects"] = projects
	}

	workspace, ok := projects["/workspace"].(map[string]interface{})
	if !ok {
		workspace = make(map[string]interface{})
		projects["/workspace"] = workspace
	}

	workspace["hasTrustDialogAccepted"] = true
	workspace["hasCompletedProjectOnboarding"] = true

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(claudeJSONPath, append(out, '\n'), 0644)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().Type() == fs.ModeSocket
}

