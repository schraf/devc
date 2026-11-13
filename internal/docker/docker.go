// Package docker builds a definition's image and manages its container through the docker CLI.
package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/schraf/devcontainer/internal/config"
	"github.com/schraf/devcontainer/internal/render"
)

const (
	// labelHash on the image is a hash of the build context it was built from, so status can
	// tell when the YAML has changed since the last build.
	labelHash = "devc.hash"
	// labelConfig on the container marks it as created by devc (and from which file), so devc
	// never touches containers it doesn't own.
	labelConfig = "devc.config"

	sshAgentSock = "/run/host-services/ssh-auth.sock"
)

// ErrNotFound is returned when an image or container doesn't exist.
var ErrNotFound = errors.New("not found")

// Client runs docker commands. Out receives command output meant for the user.
type Client struct {
	Out io.Writer
}

// BuildOptions tune a build.
type BuildOptions struct {
	NoCache bool
}

// BuildArgs is the docker invocation for cfg; the build context goes on stdin.
func BuildArgs(cfg *config.Config, hash string, opts BuildOptions) []string {
	args := []string{"buildx", "build", "--platform", cfg.Platform, "--tag", cfg.Image,
		"--label", labelHash + "=" + hash, "--progress", "plain", "--load"}
	if opts.NoCache {
		args = append(args, "--no-cache")
	}
	return append(args, "-")
}

// contextHash renders the build context and hashes it.
func contextHash(cfg *config.Config) ([]byte, string, error) {
	tarball, err := render.Context(cfg)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(tarball)
	return tarball, hex.EncodeToString(sum[:])[:16], nil
}

// Build builds cfg's image with BuildKit, streaming the build context on stdin.
func (c *Client) Build(ctx context.Context, cfg *config.Config, opts BuildOptions) error {
	tarball, hash, err := contextHash(cfg)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "docker", BuildArgs(cfg, hash, opts)...)
	cmd.Stdin = bytes.NewReader(tarball)
	cmd.Stdout, cmd.Stderr = c.Out, c.Out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}

// RunArgs is the `docker run` invocation that creates cfg's container.
func RunArgs(cfg *config.Config) ([]string, error) {
	r := cfg.Run
	args := []string{"run", "--detach", "--name", r.Container, "--hostname", r.Hostname,
		"--init", "--restart", r.Restart, "--label", labelConfig + "=" + cfg.Path}
	if r.Privileged || cfg.Enabled("docker-in-docker") {
		args = append(args, "--privileged", "--cgroupns", "private")
	}
	for _, v := range cfg.Volumes() {
		args = append(args, "--volume", v.Name+":"+v.Target)
	}
	for _, m := range r.Mounts {
		host, target, ro, err := cfg.ParseMount(m)
		if err != nil {
			return nil, err
		}
		spec := host + ":" + target
		if ro {
			spec += ":ro"
		}
		args = append(args, "--volume", spec)
	}
	if r.SSHAgent {
		args = append(args, "--volume", sshAgentSock+":"+sshAgentSock, "--env", "SSH_AUTH_SOCK="+sshAgentSock)
	}
	for _, p := range r.Ports {
		args = append(args, "--publish", p)
	}
	keys := make([]string, 0, len(r.Env))
	for k := range r.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--env", k+"="+r.Env[k])
	}
	return append(args, cfg.Image), nil
}

// Image describes the built image.
type Image struct {
	ID, Hash string
}

// Container describes the dev container.
type Container struct {
	ImageID string
	Running bool
	Status  string // e.g. running, exited
	Config  string // devc.config label; empty when not created by devc
}

// InspectImage looks up cfg's image.
func (c *Client) InspectImage(cfg *config.Config) (Image, error) {
	out, err := output("image", "inspect", "--format", `{{.Id}} {{index .Config.Labels "`+labelHash+`"}}`, cfg.Image)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such image") {
			return Image{}, ErrNotFound
		}
		return Image{}, err
	}
	id, hash, _ := strings.Cut(out, " ")
	return Image{ID: id, Hash: hash}, nil
}

// InspectContainer looks up cfg's container.
func (c *Client) InspectContainer(cfg *config.Config) (Container, error) {
	out, err := output("container", "inspect", "--format",
		`{{.Image}}|{{.State.Running}}|{{.State.Status}}|{{index .Config.Labels "`+labelConfig+`"}}`, cfg.Run.Container)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such container") {
			return Container{}, ErrNotFound
		}
		return Container{}, err
	}
	f := strings.SplitN(out, "|", 4)
	if len(f) != 4 {
		return Container{}, fmt.Errorf("unexpected docker inspect output %q", out)
	}
	return Container{ImageID: f[0], Running: f[1] == "true", Status: f[2], Config: f[3]}, nil
}

// owned returns cfg's container, refusing one that devc didn't create.
func (c *Client) owned(cfg *config.Config) (Container, error) {
	ct, err := c.InspectContainer(cfg)
	if err != nil {
		return ct, err
	}
	if ct.Config == "" {
		return ct, fmt.Errorf("container %q exists but wasn't created by devc; remove it or set run.container to another name", cfg.Run.Container)
	}
	return ct, nil
}

// UpOptions tune Up.
type UpOptions struct {
	Build bool // rebuild the image first
}

// Up makes sure cfg's container is running: it builds a missing image, creates a missing
// container, recreates one whose image was rebuilt, and starts a stopped one.
func (c *Client) Up(ctx context.Context, cfg *config.Config, opts UpOptions) error {
	img, err := c.InspectImage(cfg)
	switch {
	case opts.Build || errors.Is(err, ErrNotFound):
		if !opts.Build {
			c.printf("Image %s not found; building it.\n", cfg.Image)
		}
		if err := c.Build(ctx, cfg, BuildOptions{}); err != nil {
			return err
		}
		if img, err = c.InspectImage(cfg); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if _, hash, err := contextHash(cfg); err == nil && hash != img.Hash {
			c.printf("Note: %s changed since %s was built; run `devc up --build` to rebuild.\n", cfg.Path, cfg.Image)
		}
	}

	ct, err := c.owned(cfg)
	switch {
	case errors.Is(err, ErrNotFound):
		return c.create(cfg)
	case err != nil:
		return err
	case ct.ImageID != img.ID:
		c.printf("Recreating %s with the new image (volumes are kept).\n", cfg.Run.Container)
		if err := run("rm", "--force", cfg.Run.Container); err != nil {
			return err
		}
		return c.create(cfg)
	case ct.Running:
		c.printf("%s is already running.\n", cfg.Run.Container)
		return nil
	default:
		c.printf("Starting %s.\n", cfg.Run.Container)
		return run("start", cfg.Run.Container)
	}
}

func (c *Client) create(cfg *config.Config) error {
	args, err := RunArgs(cfg)
	if err != nil {
		return err
	}
	c.printf("Creating %s from %s.\n", cfg.Run.Container, cfg.Image)
	_, err = output(args...)
	return err
}

// Stop stops the container.
func (c *Client) Stop(cfg *config.Config) error {
	ct, err := c.owned(cfg)
	if errors.Is(err, ErrNotFound) {
		c.printf("%s doesn't exist.\n", cfg.Run.Container)
		return nil
	}
	if err != nil {
		return err
	}
	if !ct.Running {
		c.printf("%s is already stopped.\n", cfg.Run.Container)
		return nil
	}
	c.printf("Stopping %s.\n", cfg.Run.Container)
	return run("stop", cfg.Run.Container)
}

// Down removes the container, and its named volumes when volumes is set.
func (c *Client) Down(cfg *config.Config, volumes bool) error {
	_, err := c.owned(cfg)
	switch {
	case errors.Is(err, ErrNotFound):
		c.printf("%s doesn't exist.\n", cfg.Run.Container)
	case err != nil:
		return err
	default:
		c.printf("Removing %s.\n", cfg.Run.Container)
		if err := run("rm", "--force", cfg.Run.Container); err != nil {
			return err
		}
	}
	if !volumes {
		return nil
	}
	for _, v := range cfg.Volumes() {
		if _, err := output("volume", "inspect", v.Name); err != nil {
			continue
		}
		c.printf("Removing volume %s.\n", v.Name)
		if err := run("volume", "rm", v.Name); err != nil {
			return err
		}
	}
	return nil
}

// Shell replaces devc with an interactive `docker exec` in the running container: the user's
// shell, or command when given.
func (c *Client) Shell(cfg *config.Config, command []string) error {
	ct, err := c.owned(cfg)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%s doesn't exist; run `devc up` first", cfg.Run.Container)
	}
	if err != nil {
		return err
	}
	if !ct.Running {
		return fmt.Errorf("%s is %s; run `devc up` first", cfg.Run.Container, ct.Status)
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		return err
	}
	args := []string{"docker", "exec", "--interactive", "--env", "TERM", "--env", "COLORTERM",
		"--user", cfg.User.Name, "--workdir", cfg.Workdir}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		args = append(args, "--tty")
	}
	args = append(args, cfg.Run.Container)
	if len(command) == 0 {
		command = []string{cfg.User.Shell, "-l"}
	}
	return syscall.Exec(docker, append(args, command...), os.Environ())
}

// Logs shows the container's output.
func (c *Client) Logs(ctx context.Context, cfg *config.Config, follow bool) error {
	if _, err := c.owned(cfg); err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%s doesn't exist", cfg.Run.Container)
		}
		return err
	}
	args := []string{"logs"}
	if follow {
		args = append(args, "--follow")
	}
	cmd := exec.CommandContext(ctx, "docker", append(args, cfg.Run.Container)...)
	cmd.Stdout, cmd.Stderr = c.Out, c.Out
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// Status prints the image and container state.
func (c *Client) Status(cfg *config.Config) error {
	img, err := c.InspectImage(cfg)
	imgState := "built"
	switch {
	case errors.Is(err, ErrNotFound):
		imgState = "not built (devc build)"
	case err != nil:
		return err
	default:
		if _, hash, herr := contextHash(cfg); herr != nil {
			imgState = "built; the definition is invalid: " + herr.Error()
		} else if hash != img.Hash {
			imgState = "outdated: the definition changed since the last build (devc build)"
		} else {
			imgState = "up to date"
		}
	}
	c.printf("definition  %s\n", cfg.Path)
	c.printf("image       %s: %s\n", cfg.Image, imgState)

	ct, err := c.InspectContainer(cfg)
	ctState := ""
	switch {
	case errors.Is(err, ErrNotFound):
		ctState = "not created (devc up)"
	case err != nil:
		return err
	case ct.Config == "":
		ctState = ct.Status + ", but not created by devc"
	default:
		ctState = ct.Status
		if img.ID != "" && ct.ImageID != img.ID {
			ctState += "; runs an older image (devc up recreates it)"
		}
	}
	c.printf("container   %s: %s\n", cfg.Run.Container, ctState)
	for _, v := range cfg.Volumes() {
		state := "exists"
		if _, err := output("volume", "inspect", v.Name); err != nil {
			state = "not created yet"
		}
		c.printf("volume      %s -> %s (%s)\n", v.Name, v.Target, state)
	}
	return nil
}

func (c *Client) printf(format string, args ...any) {
	if c.Out != nil {
		fmt.Fprintf(c.Out, format, args...)
	}
}

// output runs docker and returns its trimmed stdout; stderr becomes the error.
func output(args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("docker %s: %s", args[0], msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func run(args ...string) error {
	_, err := output(args...)
	return err
}
