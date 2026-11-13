package docker

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/schraf/devcontainer/internal/config"
)

func TestRunArgs(t *testing.T) {
	cfg, err := config.Load("../../examples/dev.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Run.Ports = []string{"3000:3000"}
	cfg.Run.Env = map[string]string{"B": "2", "A": "1"}
	args, err := RunArgs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	home, _ := os.UserHomeDir()
	for _, want := range []string{
		"run --detach --name dev --hostname dev --init --restart unless-stopped",
		"--label devc.config=" + cfg.Path,
		"--privileged --cgroupns private",
		"--volume dev-cache:/home/dev/.cache",
		"--volume dev-local-share:/home/dev/.local/share",
		"--volume dev-go:/home/dev/go",
		"--volume dev-docker:/var/lib/docker",
		"--volume " + filepath.Join(home, "Projects") + ":/workspaces",
		"--volume /run/host-services/ssh-auth.sock:/run/host-services/ssh-auth.sock --env SSH_AUTH_SOCK=/run/host-services/ssh-auth.sock",
		"--publish 3000:3000",
		"--env A=1 --env B=2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if args[len(args)-1] != "devcontainer:latest" {
		t.Errorf("image should be last, got %q", args[len(args)-1])
	}
}

func TestRunArgsMinimal(t *testing.T) {
	cfg := config.New("tiny")
	args, err := RunArgs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(args, "--privileged") || slices.Contains(args, "--publish") {
		t.Errorf("unexpected flags: %v", args)
	}
}

func TestBuildArgs(t *testing.T) {
	cfg := config.New("tiny")
	cfg.Platform = "linux/amd64"
	got := strings.Join(BuildArgs(cfg, "abc", BuildOptions{NoCache: true}), " ")
	want := "buildx build --platform linux/amd64 --tag tiny:latest --label devc.hash=abc --progress plain --load --no-cache -"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
