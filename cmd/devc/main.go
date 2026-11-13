// Command devc builds a dev container image from a single YAML file and manages its container.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/schraf/devcontainer/internal/config"
	"github.com/schraf/devcontainer/internal/docker"
	"github.com/schraf/devcontainer/internal/features"
)

const usage = `devc - build and run a dev container defined in one YAML file

usage: devc [-f file.yaml] <command> [flags]

commands:
  build [--no-cache]     build the image
  up [--build]           create/start the container (builds the image if missing;
                         recreates the container when the image was rebuilt)
  stop                   stop the container
  down [--volumes]       remove the container (and its named volumes)
  shell [-- cmd args]    open a shell (or run a command) in the running container
  status                 show image, container and volume state
  logs [-f]              show the container's output

The definition is -f, else $DEVC_FILE, else devc.yaml or devcontainer.yaml in the
current directory.`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "devc:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	global := flag.NewFlagSet("devc", flag.ContinueOnError)
	global.Usage = func() { fmt.Fprintln(os.Stderr, usage) }
	file := global.String("f", "", "definition file")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if global.NArg() == 0 {
		global.Usage()
		return errors.New("no command given")
	}
	name, rest := global.Arg(0), global.Args()[1:]

	switch name {
	case "build", "up", "stop", "down", "shell", "status", "logs":
	case "help":
		fmt.Println(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\n%s", name, usage)
	}
	fs := flag.NewFlagSet("devc "+name, flag.ContinueOnError)
	fs.StringVar(file, "f", *file, "definition file")
	var noCache, rebuild, volumes, follow bool
	switch name {
	case "build":
		fs.BoolVar(&noCache, "no-cache", false, "build without the layer cache")
	case "up":
		fs.BoolVar(&rebuild, "build", false, "rebuild the image first")
	case "down":
		fs.BoolVar(&volumes, "volumes", false, "also remove the named volumes")
	case "logs":
		fs.BoolVar(&follow, "follow", false, "follow the log output")
	}
	if name == "logs" { // `devc logs -f` means follow, as with docker logs
		for i, a := range rest {
			if a == "-f" {
				rest[i] = "-follow"
			}
		}
	}
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 && name != "shell" {
		return fmt.Errorf("unexpected arguments %q", fs.Args())
	}

	cfg, err := load(*file)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	c := &docker.Client{Out: os.Stdout}
	switch name {
	case "build":
		return c.Build(ctx, cfg, docker.BuildOptions{NoCache: noCache})
	case "up":
		return c.Up(ctx, cfg, docker.UpOptions{Build: rebuild})
	case "stop":
		return c.Stop(cfg)
	case "down":
		return c.Down(cfg, volumes)
	case "shell":
		return c.Shell(cfg, fs.Args())
	case "status":
		return c.Status(cfg)
	case "logs":
		return c.Logs(ctx, cfg, follow)
	}
	return nil
}

// load finds, parses and validates the definition.
func load(file string) (*config.Config, error) {
	if file == "" {
		file = os.Getenv("DEVC_FILE")
	}
	if file == "" {
		for _, f := range []string{"devc.yaml", "devcontainer.yaml"} {
			if _, err := os.Stat(f); err == nil {
				file = f
				break
			}
		}
	}
	if file == "" {
		return nil, errors.New("no definition found: pass -f file.yaml, set DEVC_FILE, or add devc.yaml here")
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(abs)
	if err != nil {
		return nil, err
	}
	if err := features.Validate(cfg); err != nil {
		return nil, fmt.Errorf("%s:\n%w", file, err)
	}
	return cfg, nil
}
