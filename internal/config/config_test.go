package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaults(t *testing.T) {
	c, err := Parse([]byte("name: x\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Image != "x:latest" || c.Base == "" || c.User.Name != "dev" || c.Workdir != "/workspaces" {
		t.Errorf("defaults not applied: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("defaults should validate: %v", err)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	if _, err := Parse([]byte("name: x\nfeaturez: {}\n")); err == nil {
		t.Error("expected an error for an unknown top-level key")
	}
}

func TestValidate(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"root user":    func(c *Config) { c.User.Name = "root" },
		"bad platform": func(c *Config) { c.Platform = "linux/riscv64" },
		"abs persist":  func(c *Config) { c.Persist = []string{"/etc"} },
		"escape":       func(c *Config) { c.Persist = []string{"../x"} },
		"file outside": func(c *Config) { c.Files = map[string]string{"../x": ""} },
		"file abs":     func(c *Config) { c.Files = map[string]string{"/etc/x": ""} },
		"file unclean": func(c *Config) { c.Files = map[string]string{"a/../../x": ""} },
		"env name":     func(c *Config) { c.Env = map[string]string{"1X": "y"} },
		"image spaces": func(c *Config) { c.Image = "a b" },
		"relative wd":  func(c *Config) { c.Workdir = "work" },
		"not a .crt":   func(c *Config) { c.Certs = []string{"ca.pem"} },
		"zero uid":     func(c *Config) { c.User.UID = 0 },
	} {
		c := New("t")
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestRunDefaultsAndVolumes(t *testing.T) {
	c, err := Parse([]byte("name: box\npersist: [.cache, .local/share, go]\nfeatures: {docker-in-docker: {}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Run.Container != "box" || c.Run.Hostname != "box" || c.Run.Restart != "unless-stopped" {
		t.Errorf("run defaults = %+v", c.Run)
	}
	want := []Volume{
		{"box-cache", "/home/dev/.cache"},
		{"box-local-share", "/home/dev/.local/share"},
		{"box-go", "/home/dev/go"},
		{"box-docker", "/var/lib/docker"},
	}
	if got := c.Volumes(); !reflect.DeepEqual(got, want) {
		t.Errorf("volumes = %v, want %v", got, want)
	}
}

func TestParseMount(t *testing.T) {
	c := New("t")
	c.Path = "/defs/box.yaml"
	home, _ := os.UserHomeDir()
	for in, want := range map[string][3]any{
		"~/Projects:/workspaces": {filepath.Join(home, "Projects"), "/workspaces", false},
		"src:/src:ro":            {"/defs/src", "/src", true},
		"/abs:/x":                {"/abs", "/x", false},
	} {
		host, target, ro, err := c.ParseMount(in)
		if err != nil || host != want[0] || target != want[1] || ro != want[2] {
			t.Errorf("ParseMount(%q) = %s %s %v %v", in, host, target, ro, err)
		}
	}
}
