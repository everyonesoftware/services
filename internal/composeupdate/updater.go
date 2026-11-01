package composeupdate

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"services/internal/groups"
)

type Runner interface {
	Run(context.Context, string, []string) error
}

type Updater struct {
	hostRoot     string
	mountRoot    string
	dockerSocket string
	runner       Runner
	mu           sync.Mutex
}

func New(hostRoot, mountRoot, dockerSocket string) *Updater {
	return NewWithRunner(hostRoot, mountRoot, dockerSocket, execRunner{dockerSocket: dockerSocket})
}

func NewWithRunner(hostRoot, mountRoot, dockerSocket string, runner Runner) *Updater {
	return &Updater{
		hostRoot:     hostRoot,
		mountRoot:    mountRoot,
		dockerSocket: dockerSocket,
		runner:       runner,
	}
}

func (u *Updater) Pull(ctx context.Context, container groups.Container) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.run(ctx, container, "pull", container.Service)
}

func (u *Updater) Update(ctx context.Context, container groups.Container) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if err := u.run(ctx, container, "pull", container.Service); err != nil {
		return err
	}
	return u.run(ctx, container,
		"up", "-d", "--no-deps", "--pull", "never", "--no-build", "--wait", "--wait-timeout", "300", container.Service,
	)
}

func (u *Updater) run(ctx context.Context, container groups.Container, command ...string) error {
	if u.runner == nil {
		return fmt.Errorf("Compose command runner is not configured")
	}
	workingDir, args, err := u.composeArgs(container)
	if err != nil {
		return err
	}
	args = append(args, command...)
	if err := u.runner.Run(ctx, workingDir, args); err != nil {
		return fmt.Errorf("Compose operation %q failed: %w", command[0], err)
	}
	return nil
}

func (u *Updater) composeArgs(container groups.Container) (string, []string, error) {
	if strings.TrimSpace(u.hostRoot) == "" || strings.TrimSpace(u.mountRoot) == "" {
		return "", nil, fmt.Errorf("Compose project mount is not configured")
	}
	if !projectNamePattern.MatchString(container.Project) || !serviceNamePattern.MatchString(container.Service) {
		return "", nil, fmt.Errorf("container is missing a valid Compose project or service label")
	}
	configFiles, err := u.resolveConfigFiles(container)
	if err != nil {
		return "", nil, err
	}
	workingDirHost := container.ComposeWorkingDir
	if strings.TrimSpace(workingDirHost) == "" {
		workingDirHost = filepath.Dir(configFiles[0])
	}
	workingDir := workingDirHost
	if strings.TrimSpace(container.ComposeWorkingDir) != "" {
		workingDir, err = u.resolveProjectPath(workingDirHost, true)
		if err != nil {
			return "", nil, fmt.Errorf("resolve Compose working directory: %w", err)
		}
	}
	args := []string{"--project-name", container.Project, "--project-directory", workingDir}
	for _, configFile := range configFiles {
		args = append(args, "--file", configFile)
	}
	return workingDir, args, nil
}

func (u *Updater) resolveConfigFiles(container groups.Container) ([]string, error) {
	if container.ComposeConfigFilesLabel != "" {
		return u.resolveConfigFilesLabel(container.ComposeConfigFilesLabel)
	}
	if len(container.ComposeConfigFiles) == 0 {
		return nil, fmt.Errorf("container has no Compose configuration file labels")
	}
	resolved := make([]string, 0, len(container.ComposeConfigFiles))
	for _, configFile := range container.ComposeConfigFiles {
		path, err := u.resolveProjectPath(configFile, false)
		if err != nil {
			return nil, fmt.Errorf("resolve Compose configuration file: %w", err)
		}
		resolved = append(resolved, path)
	}
	return resolved, nil
}

func (u *Updater) resolveConfigFilesLabel(label string) ([]string, error) {
	segments := strings.Split(strings.TrimSpace(label), ",")
	if len(segments) == 0 || len(segments) > 32 {
		return nil, fmt.Errorf("Compose configuration file label has an invalid number of paths")
	}
	type candidate struct {
		end      int
		resolved string
	}
	candidates := make([][]candidate, len(segments))
	for start := range segments {
		for end := start; end < len(segments); end++ {
			hostPath := strings.TrimSpace(strings.Join(segments[start:end+1], ","))
			resolved, err := u.resolveProjectPath(hostPath, false)
			if err == nil {
				candidates[start] = append(candidates[start], candidate{end: end, resolved: resolved})
			}
		}
	}

	memo := make(map[int][][]string)
	var parse func(int) [][]string
	parse = func(start int) [][]string {
		if start == len(segments) {
			return [][]string{{}}
		}
		if cached, ok := memo[start]; ok {
			return cached
		}
		solutions := make([][]string, 0, 2)
		for _, file := range candidates[start] {
			for _, suffix := range parse(file.end + 1) {
				sequence := make([]string, 1, len(suffix)+1)
				sequence[0] = file.resolved
				sequence = append(sequence, suffix...)
				solutions = append(solutions, sequence)
				if len(solutions) == 2 {
					memo[start] = solutions
					return solutions
				}
			}
		}
		memo[start] = solutions
		return solutions
	}

	solutions := parse(0)
	switch len(solutions) {
	case 0:
		return nil, fmt.Errorf("Compose configuration files do not resolve beneath the configured root")
	case 1:
		return solutions[0], nil
	default:
		return nil, fmt.Errorf("Compose configuration file label is ambiguous")
	}
}

func (u *Updater) resolveProjectPath(hostPath string, wantDirectory bool) (string, error) {
	hostRoot, windowsPaths, err := normalizeHostPath(u.hostRoot)
	if err != nil {
		return "", fmt.Errorf("configured Compose root: %w", err)
	}
	candidate, candidateUsesWindowsPaths, err := normalizeHostPath(hostPath)
	if err != nil {
		return "", fmt.Errorf("Compose host path: %w", err)
	}
	if windowsPaths != candidateUsesWindowsPaths {
		return "", fmt.Errorf("Compose host path does not use the configured root's path format")
	}
	relative, err := relativeHostPath(hostRoot, candidate, windowsPaths)
	if err != nil {
		return "", err
	}

	root, err := filepath.Abs(u.mountRoot)
	if err != nil {
		return "", fmt.Errorf("resolve mounted Compose root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve mounted Compose root: %w", err)
	}
	resolved := filepath.Join(root, filepath.FromSlash(relative))
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve mounted Compose path: %w", err)
	}
	if !within(root, resolved) {
		return "", fmt.Errorf("mounted Compose path escapes the configured root")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat mounted Compose path: %w", err)
	}
	if wantDirectory && !info.IsDir() {
		return "", fmt.Errorf("Compose working directory is not a directory")
	}
	if !wantDirectory && !info.Mode().IsRegular() {
		return "", fmt.Errorf("Compose configuration path is not a regular file")
	}
	return resolved, nil
}

func normalizeHostPath(value string) (string, bool, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	if len(value) >= 2 && isASCIIAlpha(value[0]) && value[1] == ':' {
		if len(value) < 3 || value[2] != '/' {
			return "", false, fmt.Errorf("path is not absolute")
		}
		drive := strings.ToUpper(value[:1]) + ":"
		tail := path.Clean("/" + strings.TrimLeft(value[2:], "/"))
		return drive + tail, true, nil
	}
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "", false, fmt.Errorf("path is not an absolute drive or Unix path")
	}
	return path.Clean(value), false, nil
}

func isASCIIAlpha(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func relativeHostPath(root, candidate string, windowsPaths bool) (string, error) {
	rootParts := hostPathParts(root)
	candidateParts := hostPathParts(candidate)
	if len(candidateParts) < len(rootParts) {
		return "", fmt.Errorf("path is outside the configured Compose root")
	}
	for index, rootPart := range rootParts {
		matches := candidateParts[index] == rootPart
		if windowsPaths {
			matches = windowsPathComponentEqual(candidateParts[index], rootPart)
		}
		if !matches {
			return "", fmt.Errorf("path is outside the configured Compose root")
		}
	}
	if len(candidateParts) == len(rootParts) {
		return ".", nil
	}
	return strings.Join(candidateParts[len(rootParts):], "/"), nil
}

func windowsPathComponentEqual(left, right string) bool {
	if isASCIIPathComponent(left) && isASCIIPathComponent(right) {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func isASCIIPathComponent(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] > 0x7f {
			return false
		}
	}
	return true
}

func hostPathParts(value string) []string {
	value = strings.Trim(value, "/")
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}

func within(root, target string) bool {
	// These paths are in the Services Linux-container namespace; filepath.Rel
	// must use that case-sensitive namespace, not Windows host-label rules.
	relative, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

var projectNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
var serviceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type execRunner struct {
	dockerSocket string
}

func (r execRunner) Run(ctx context.Context, workingDir string, composeArgs []string) error {
	if r.dockerSocket == "" {
		return fmt.Errorf("Docker socket is not configured")
	}
	if !path.IsAbs(r.dockerSocket) {
		return fmt.Errorf("Docker socket path must be absolute inside the Services container")
	}
	args := []string{"--host", "unix://" + r.dockerSocket, "compose"}
	args = append(args, composeArgs...)
	command := exec.CommandContext(ctx, "docker", args...)
	command.Dir = workingDir
	command.Env = composeEnvironment(os.Environ())
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return fmt.Errorf("docker compose command failed: %w", err)
	}
	return nil
}

func composeEnvironment(_ []string) []string {
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/tmp",
	}
}
