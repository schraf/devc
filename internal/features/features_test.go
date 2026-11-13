package features

import (
	"strings"
	"testing"

	"github.com/schraf/devcontainer/internal/config"
)

func TestEveryFeatureRendersOnBothPlatforms(t *testing.T) {
	for _, platform := range []string{"linux/arm64", "linux/amd64"} {
		cfg := config.New("t")
		cfg.Platform = platform
		for _, f := range All() {
			cfg.Features[f.Name] = config.FeatureOptions{}
		}
		for _, f := range All() {
			for _, s := range []Stage{StageSystem, StageUser, StageLate} {
				out, err := f.Render(cfg, s)
				if err != nil {
					t.Errorf("%s %s stage %d: %v", platform, f.Name, s, err)
				}
				if strings.Contains(out, "<no value>") {
					t.Errorf("%s %s: template printed <no value>", platform, f.Name)
				}
			}
		}
	}
}

func TestArchSpelling(t *testing.T) {
	cfg := config.New("t")
	cfg.Platform = "linux/amd64"
	cfg.Features["starship"] = nil
	f, _ := Get("starship")
	out, err := f.Render(cfg, StageSystem)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "starship-x86_64-unknown-linux-musl") {
		t.Errorf("amd64 starship asset not used:\n%s", out)
	}
}

func TestParseRepo(t *testing.T) {
	for spec, want := range map[string]Repo{
		"fatih/vim-go":                         {URL: "https://github.com/fatih/vim-go", Name: "vim-go"},
		"fatih/vim-go@v1.28":                   {URL: "https://github.com/fatih/vim-go", Name: "vim-go", Ref: "v1.28"},
		"https://gitlab.com/a/b.git@main":      {URL: "https://gitlab.com/a/b.git", Name: "b", Ref: "main"},
		"git@github.com:junegunn/fzf.git":      {URL: "git@github.com:junegunn/fzf.git", Name: "fzf"},
		"git@github.com:junegunn/fzf.git@v0.1": {URL: "git@github.com:junegunn/fzf.git", Name: "fzf", Ref: "v0.1"},
	} {
		got, err := ParseRepo(spec)
		if err != nil || got != want {
			t.Errorf("ParseRepo(%q) = %+v, %v; want %+v", spec, got, err, want)
		}
	}
	for _, bad := range []string{"vim-go", "a/b/c", "a/b@$(x)", "/abs/path"} {
		if _, err := ParseRepo(bad); err == nil {
			t.Errorf("ParseRepo(%q) should fail", bad)
		}
	}
}

func TestVimGoTools(t *testing.T) {
	cfg := config.New("t")
	cfg.Features["vim"] = config.FeatureOptions{"plugins": []any{"fatih/vim-go@v1.29"}}
	f, _ := Get("vim")
	out, err := f.Render(cfg, StageUser)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--branch v1.29 https://github.com/fatih/vim-go vim-go") {
		t.Errorf("plugin not cloned at its ref:\n%s", out)
	}
	if strings.Contains(out, "GoInstallBinaries") {
		t.Errorf("vim-go tools need the go feature:\n%s", out)
	}
	cfg.Features["go"] = nil
	if out, _ = f.Render(cfg, StageUser); !strings.Contains(out, "GoInstallBinaries") {
		t.Errorf("vim-go tools missing with go enabled:\n%s", out)
	}
}

func TestDockerInDockerNeedsSudo(t *testing.T) {
	cfg := config.New("t")
	cfg.Features["docker-in-docker"] = nil
	if err := Validate(cfg); err != nil {
		t.Fatalf("default user has sudo: %v", err)
	}
	cfg.User.Sudo = false
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "user.sudo") {
		t.Errorf("want a user.sudo error, got %v", err)
	}
}

func TestCatalogEntries(t *testing.T) {
	cfg := config.New("t")
	for _, f := range All() {
		if f.Description == "" {
			t.Errorf("%s: no description", f.Name)
		}
		if len(f.Apt) == 0 && !f.HasStage(StageSystem) && !f.HasStage(StageUser) && !f.HasStage(StageLate) {
			t.Errorf("%s: installs nothing", f.Name)
		}
		// Defaults must satisfy their own types and patterns.
		cfg.Features[f.Name] = nil
		if _, err := f.Values(cfg); err != nil {
			t.Errorf("%s defaults: %v", f.Name, err)
		}
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	if _, err := parse("x", []byte("description: x\napt: [a]\nsytem: RUN true\n")); err == nil {
		t.Error("misspelled key should fail")
	}
	if _, err := parse("x", []byte("description: x\nsystem: '{{.Opt.version'\n")); err == nil {
		t.Error("bad template should fail")
	}
}
