package main

import (
	"bufio"
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

	"github.com/manifoldco/promptui"
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
	rootCmd := &cobra.Command{
		Use:           "devcontainer",
		Short:         "Manage Claude devcontainers",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.AddCommand(newStartCmd())
	rootCmd.AddCommand(newExecCmd())

	if err := rootCmd.Execute(); err != nil {
		var ec exitCodeError
		if errors.As(err, &ec) {
			os.Exit(ec.code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newStartCmd() *cobra.Command {
	var flagName string
	var flagVCS string
	var flagDocker bool
	var flagPorts []string
	var flagResume string

	cmd := &cobra.Command{
		Use:   "start [flags] [-- command...]",
		Short: "Launch a new Claude devcontainer",
		Long:  "Creates a Docker container with Claude Code and development tools, using VCS worktrees for isolation.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(flagName, flagVCS, flagDocker, flagPorts, flagResume, args)
		},
	}

	cmd.Flags().StringVar(&flagName, "name", "", "name for worktree/container (default: random suffix)")
	cmd.Flags().StringVar(&flagVCS, "vcs", "", "override VCS type: git or jj (default: auto-detect)")
	cmd.Flags().BoolVar(&flagDocker, "docker", false, "mount Docker socket into the container")
	cmd.Flags().StringArrayVar(&flagPorts, "port", nil, "publish a container port to the host (hostPort:containerPort)")
	cmd.Flags().StringVar(&flagResume, "resume", "", "resume a Claude session by ID or name")
	cmd.Flags().Lookup("resume").NoOptDefVal = " "

	return cmd
}

type containerInfo struct {
	ID    string `json:"ID"`
	Names string `json:"Names"`
}

func newExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec [container-name]",
		Short: "Attach to a running devcontainer",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var target string
			if len(args) > 0 {
				target = args[0]
			}
			workspaceDir := os.Getenv("BUILD_WORKSPACE_DIRECTORY")
			if workspaceDir == "" {
				var err error
				workspaceDir, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("getting working directory: %w", err)
				}
				workspaceDir = findVCSRoot(workspaceDir)
			}
			name, err := resolveContainer(target, workspaceDir)
			if err != nil {
				return err
			}
			return runExec(name)
		},
	}
}

func listDevcontainers(workspaceDir string) ([]containerInfo, error) {
	args := []string{"ps",
		"--filter", "name=devcontainer-",
		"--filter", "name=claude-dev",
	}
	if workspaceDir != "" {
		args = append(args, "--filter", "label=claude-devcontainer.workspace="+workspaceDir)
	}
	args = append(args, "--format", "{{json .}}")

	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	var containers []containerInfo
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ci containerInfo
		if err := json.Unmarshal([]byte(line), &ci); err != nil {
			continue
		}
		containers = append(containers, ci)
	}
	return containers, nil
}

func resolveContainer(target, workspaceDir string) (string, error) {
	containers, err := listDevcontainers(workspaceDir)
	if err != nil {
		return "", err
	}
	if len(containers) == 0 {
		return "", fmt.Errorf("no running devcontainers found")
	}

	if target != "" {
		for _, c := range containers {
			if c.Names == target || c.Names == "devcontainer-"+target {
				return c.Names, nil
			}
		}
		return "", fmt.Errorf("no running devcontainer matching %q", target)
	}

	if len(containers) == 1 {
		return containers[0].Names, nil
	}

	return promptSelectContainer(containers)
}

func promptSelectContainer(containers []containerInfo) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("multiple devcontainers running; specify a name or run interactively")
	}

	items := make([]string, len(containers))
	for i, c := range containers {
		items[i] = c.Names
	}

	prompt := promptui.Select{
		Label:  "Select a devcontainer",
		Items:  items,
		Stdout: os.Stderr,
	}

	idx, _, err := prompt.Run()
	if err != nil {
		return "", fmt.Errorf("selection: %w", err)
	}
	return containers[idx].Names, nil
}

func runExec(containerName string) error {
	dockerArgs := []string{"exec", "-i"}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		dockerArgs = append(dockerArgs, "-t")
	}
	dockerArgs = append(dockerArgs, containerName, "bash")

	dockerCmd := exec.Command("docker", dockerArgs...)
	dockerCmd.Stdin = os.Stdin
	dockerCmd.Stdout = os.Stdout
	dockerCmd.Stderr = os.Stderr

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	if err := dockerCmd.Start(); err != nil {
		return fmt.Errorf("starting docker exec: %w", err)
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
			return fmt.Errorf("running docker exec: %w", err)
		}
	}

	signal.Stop(sigCh)
	close(sigCh)

	if exitCode != 0 {
		return exitCodeError{code: exitCode}
	}
	return nil
}

func run(name, vcsFlag string, docker bool, ports []string, resume string, extraArgs []string) error {
	if resume != "" && len(extraArgs) > 0 {
		return fmt.Errorf("cannot combine --resume with extra command arguments")
	}

	// Validate port mappings
	for _, p := range ports {
		parts := strings.SplitN(p, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid port format %q: expected hostPort:containerPort", p)
		}
		if _, err := strconv.Atoi(parts[0]); err != nil {
			return fmt.Errorf("invalid host port in %q: %w", p, err)
		}
		if _, err := strconv.Atoi(parts[1]); err != nil {
			return fmt.Errorf("invalid container port in %q: %w", p, err)
		}
	}

	containerName := envOrDefault("CONTAINER_NAME", "claude-dev")
	imageName := envOrDefault("IMAGE_NAME", "claude-devcontainer")

	workspaceDir := os.Getenv("BUILD_WORKSPACE_DIRECTORY")
	if workspaceDir == "" {
		var err error
		workspaceDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		workspaceDir = findVCSRoot(workspaceDir)
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
	bazelWorkspace := workspaceDir
	if originalWorkspace != "" {
		bazelWorkspace = originalWorkspace
	}
	if fileExists(filepath.Join(bazelWorkspace, "MODULE.bazel")) {
		cmd := exec.Command("bazel", "info", "output_base")
		cmd.Dir = bazelWorkspace
		out, err := cmd.Output()
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

	// Docker socket (opt-in)
	if docker && isSocket(dockerSock) {
		addMount(dockerSock, dockerSock, false)
	}

	// Conditional mounts
	if fileExists(filepath.Join(homeDir, ".gitconfig")) {
		addMount(filepath.Join(homeDir, ".gitconfig"), devHome+"/.gitconfig", true)
	}
	if isDir(filepath.Join(homeDir, ".config/gh")) {
		addMount(filepath.Join(homeDir, ".config/gh"), devHome+"/.config/gh", true)
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
			// If jj uses a git backend, also mount the git repo it points to.
			gitTargetFile := filepath.Join(originalWorkspace, ".jj", "repo", "store", "git_target")
			if data, err := os.ReadFile(gitTargetFile); err == nil {
				target := strings.TrimSpace(string(data))
				if !filepath.IsAbs(target) {
					target = filepath.Join(originalWorkspace, ".jj", "repo", "store", target)
				}
				target = filepath.Clean(target)
				// Only mount if not already under .jj/repo (which is already mounted)
				jjRepo := filepath.Clean(filepath.Join(originalWorkspace, ".jj", "repo"))
				if !strings.HasPrefix(target, jjRepo+string(filepath.Separator)) && target != jjRepo {
					if isDir(target) {
						addMount(target, target, false)
					}
				}
			}
		}
	}

	// Determine source repository for labeling
	sourceWorkspace := workspaceDir
	if originalWorkspace != "" {
		sourceWorkspace = originalWorkspace
	}

	// Build docker run args
	dockerArgs := []string{"run", "--rm", "-i",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--label", "claude-devcontainer.workspace=" + sourceWorkspace,
		"--name", containerName,
	}

	// Allocate TTY if stdin is a terminal
	if term.IsTerminal(int(os.Stdin.Fd())) {
		dockerArgs = append(dockerArgs, "-t")
	}

	dockerArgs = append(dockerArgs, mounts...)
	dockerArgs = append(dockerArgs, envArgs...)
	for _, p := range ports {
		dockerArgs = append(dockerArgs, "-p", p)
	}
	dockerArgs = append(dockerArgs, imageName)
	if resume != "" {
		dockerArgs = append(dockerArgs, "claude", "--dangerously-skip-permissions", "--resume")
		if strings.TrimSpace(resume) != "" {
			dockerArgs = append(dockerArgs, resume)
		}
	} else {
		dockerArgs = append(dockerArgs, extraArgs...)
	}

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

// findVCSRoot walks up from dir looking for a .jj or .git directory,
// returning the containing directory. Returns dir unchanged if no VCS root
// is found.
func findVCSRoot(dir string) string {
	cur := dir
	for {
		if isDir(filepath.Join(cur, ".jj")) || isDir(filepath.Join(cur, ".git")) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return dir
		}
		cur = parent
	}
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

