// Package config defines the devc YAML schema: one file fully describes one dev container image.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is a single dev container definition.
type Config struct {
	Name            string                    `yaml:"name"`
	Image           string                    `yaml:"image"`
	Base            string                    `yaml:"base"`
	Platform        string                    `yaml:"platform,omitempty"`
	User            User                      `yaml:"user"`
	Timezone        string                    `yaml:"timezone,omitempty"`
	Env             map[string]string         `yaml:"env,omitempty"`
	Certs           []string                  `yaml:"certs,omitempty"`
	Features        map[string]FeatureOptions `yaml:"features,omitempty"`
	Files           map[string]string         `yaml:"files,omitempty"` // path in $HOME -> content
	Persist         []string                  `yaml:"persist,omitempty"`
	Workdir         string                    `yaml:"workdir,omitempty"`
	ExtraDockerfile string                    `yaml:"extra_dockerfile,omitempty"`
	Run             Run                       `yaml:"run,omitempty"`

	// Path is where the definition was loaded from.
	Path string `yaml:"-"`
}

// Run is how `devc up` runs the container.
type Run struct {
	Container  string            `yaml:"container,omitempty"`  // default: name
	Hostname   string            `yaml:"hostname,omitempty"`   // default: container
	Mounts     []string          `yaml:"mounts,omitempty"`     // host:container[:ro]; host may use ~ or be relative to the YAML
	Ports      []string          `yaml:"ports,omitempty"`      // as for docker run -p
	Env        map[string]string `yaml:"env,omitempty"`        // runtime-only environment
	SSHAgent   bool              `yaml:"ssh_agent,omitempty"`  // forward Docker Desktop / OrbStack's SSH agent
	Restart    string            `yaml:"restart,omitempty"`    // docker restart policy, default unless-stopped
	Privileged bool              `yaml:"privileged,omitempty"` // Privileged is forced on when docker-in-docker is enabled.
}

// User is the non-root account the container is used as.
type User struct {
	Name  string `yaml:"name"`
	UID   int    `yaml:"uid"`
	GID   int    `yaml:"gid"`
	Shell string `yaml:"shell,omitempty"`
	Sudo  bool   `yaml:"sudo"`
}

var (
	userPattern      = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
	pathPattern      = regexp.MustCompile(`^[0-9A-Za-z._/@+-]+$`)
	imagePattern     = regexp.MustCompile(`^[0-9A-Za-z._/:@-]+$`)
	envPattern       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	containerPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
	portPattern      = regexp.MustCompile(`^([0-9.]+:)?([0-9]+(-[0-9]+)?:)?[0-9]+(-[0-9]+)?(/(tcp|udp))?$`)
)

// FeatureOptions holds a feature's options exactly as written in YAML. A feature that is listed
// with no options (`go: {}` or `go:`) is enabled with its defaults.
type FeatureOptions map[string]any

// New returns a definition with sensible defaults for a fresh dev container.
func New(name string) *Config {
	c := &Config{Name: name}
	c.ApplyDefaults()
	return c
}

// ApplyDefaults fills in required fields that were left empty.
func (c *Config) ApplyDefaults() {
	if c.Name == "" {
		c.Name = "dev"
	}
	if c.Image == "" {
		c.Image = c.Name + ":latest"
	}
	if c.Base == "" {
		c.Base = "ubuntu:24.04"
	}
	if c.Platform == "" {
		c.Platform = "linux/" + runtime.GOARCH
	}
	if c.User.Name == "" {
		c.User = User{Name: "dev", UID: 1000, GID: 1000, Sudo: true}
	}
	if c.User.Shell == "" {
		c.User.Shell = "/bin/bash"
	}
	if c.Workdir == "" {
		c.Workdir = "/workspaces"
	}
	if c.Features == nil {
		c.Features = map[string]FeatureOptions{}
	}
	if c.Run.Container == "" {
		c.Run.Container = c.Name
	}
	if c.Run.Hostname == "" {
		c.Run.Hostname = c.Run.Container
	}
	if c.Run.Restart == "" {
		c.Run.Restart = "unless-stopped"
	}
}

// Load reads and parses a definition. Defaults are applied for anything left out.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.Path = path
	return c, nil
}

// Parse decodes a definition from YAML bytes.
func Parse(data []byte) (*Config, error) {
	c := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	c.ApplyDefaults()
	return c, nil
}

// Dir is the directory relative paths (certs) are resolved against.
func (c *Config) Dir() string {
	if c.Path == "" {
		return "."
	}
	return filepath.Dir(c.Path)
}

// ResolvePath resolves p relative to the definition file.
func (c *Config) ResolvePath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Dir(), p)
}

// Enabled reports whether a feature is listed.
func (c *Config) Enabled(name string) bool {
	_, ok := c.Features[name]
	return ok
}

// Arch is the CPU architecture part of Platform (e.g. arm64, amd64).
func (c *Config) Arch() string {
	_, arch, _ := strings.Cut(c.Platform, "/")
	return arch
}

// Validate checks the fields that don't depend on the feature catalog.
func (c *Config) Validate() error {
	var errs []error
	if c.Name == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if c.Image == "" {
		errs = append(errs, errors.New("image is required"))
	}
	if c.Base == "" {
		errs = append(errs, errors.New("base is required"))
	}
	switch c.Arch() {
	case "arm64", "amd64":
	default:
		errs = append(errs, fmt.Errorf("platform %q: only linux/arm64 and linux/amd64 are supported", c.Platform))
	}
	if !imagePattern.MatchString(c.Image) || !imagePattern.MatchString(c.Base) {
		errs = append(errs, errors.New("image and base must be image references (no spaces)"))
	}
	if !userPattern.MatchString(c.User.Name) || c.User.Name == "root" {
		errs = append(errs, fmt.Errorf("user.name %q must be a valid non-root user name", c.User.Name))
	}
	if c.User.UID <= 0 || c.User.GID <= 0 {
		errs = append(errs, errors.New("user.uid and user.gid must be positive"))
	}
	if !pathPattern.MatchString(c.User.Shell) || !filepath.IsAbs(c.User.Shell) {
		errs = append(errs, fmt.Errorf("user.shell %q must be an absolute path", c.User.Shell))
	}
	if !pathPattern.MatchString(c.Workdir) || !filepath.IsAbs(c.Workdir) {
		errs = append(errs, fmt.Errorf("workdir %q must be an absolute path", c.Workdir))
	}
	if c.Timezone != "" && !pathPattern.MatchString(c.Timezone) {
		errs = append(errs, fmt.Errorf("timezone %q is not valid", c.Timezone))
	}
	for _, cert := range c.Certs {
		if !strings.HasSuffix(cert, ".crt") {
			errs = append(errs, fmt.Errorf("cert %s: must be a PEM file ending in .crt", cert))
		} else if _, err := os.Stat(c.ResolvePath(cert)); err != nil {
			errs = append(errs, fmt.Errorf("cert %s: %w", cert, err))
		}
	}
	for p := range c.Files {
		if !homePath(p) {
			errs = append(errs, fmt.Errorf("files %q: must be a path inside the user's home, e.g. .config/fish/config.fish", p))
		}
	}
	for k := range c.Env {
		if !envPattern.MatchString(k) {
			errs = append(errs, fmt.Errorf("env %q: not a valid variable name", k))
		}
	}
	if !containerPattern.MatchString(c.Run.Container) || !containerPattern.MatchString(c.Run.Hostname) {
		errs = append(errs, fmt.Errorf("run.container/run.hostname %q/%q: only letters, digits, '.', '_' and '-'", c.Run.Container, c.Run.Hostname))
	}
	for _, m := range c.Run.Mounts {
		if _, _, _, err := c.ParseMount(m); err != nil {
			errs = append(errs, err)
		}
	}
	for _, p := range c.Run.Ports {
		if !portPattern.MatchString(p) {
			errs = append(errs, fmt.Errorf("run.ports %q: use [ip:]host:container[/tcp|udp]", p))
		}
	}
	for k := range c.Run.Env {
		if !envPattern.MatchString(k) {
			errs = append(errs, fmt.Errorf("run.env %q: not a valid variable name", k))
		}
	}
	switch c.Run.Restart {
	case "no", "always", "unless-stopped", "on-failure":
	default:
		errs = append(errs, fmt.Errorf("run.restart %q: use no, always, unless-stopped or on-failure", c.Run.Restart))
	}
	for _, p := range c.Persist {
		if !homePath(p) {
			errs = append(errs, fmt.Errorf("persist %q: must be a path inside the user's home", p))
		}
	}
	return errors.Join(errs...)
}

// ParseMount splits a run.mounts entry into an absolute host path, container path and whether
// it is read-only.
func (c *Config) ParseMount(m string) (host, target string, ro bool, err error) {
	parts := strings.Split(m, ":")
	if len(parts) == 3 && parts[2] == "ro" {
		ro, parts = true, parts[:2]
	}
	if len(parts) != 2 || parts[0] == "" || !filepath.IsAbs(parts[1]) {
		return "", "", false, fmt.Errorf("run.mounts %q: use host-path:/container/path[:ro]", m)
	}
	host, err = filepath.Abs(c.ResolvePath(parts[0]))
	return host, parts[1], ro, err
}

// Volume is a named volume the container mounts.
type Volume struct {
	Name, Target string
}

// Volumes lists the named volumes: one per persist entry (in the user's home), plus the
// inner Docker's data when docker-in-docker is enabled. Names are prefixed with the container
// name so several dev containers don't share state.
func (c *Config) Volumes() []Volume {
	var vols []Volume
	home := "/home/" + c.User.Name
	for _, p := range c.Persist {
		slug := strings.Trim(strings.NewReplacer("/", "-", ".", "").Replace(filepath.Clean(p)), "-")
		vols = append(vols, Volume{Name: c.Run.Container + "-" + slug, Target: filepath.Join(home, p)})
	}
	if c.Enabled("docker-in-docker") {
		vols = append(vols, Volume{Name: c.Run.Container + "-docker", Target: "/var/lib/docker"})
	}
	return vols
}

// homePath reports whether p is a clean relative path that stays inside $HOME.
func homePath(p string) bool {
	return p != "" && !filepath.IsAbs(p) && filepath.Clean(p) == p && p != "." &&
		!strings.HasPrefix(p, "../") && p != ".." && pathPattern.MatchString(p)
}
