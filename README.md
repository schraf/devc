# devc

`devc` builds a dev container image from a single YAML file and manages the container it runs
in. The file describes everything: the base image, the user, the software to install
(features), your own config files, and how the container runs (mounts, ports,
persisted directories).

devc turns the YAML into a Dockerfile in memory and streams the build context to
`docker buildx build -`. It then manages the container with plain `docker` commands. Nothing
is written next to your YAML, and there's no compose file. The only runtime dependency is the
docker CLI.

## Requirements
- Docker with BuildKit and buildx: Docker Desktop, OrbStack, or Docker Engine with the buildx
  plugin.
- Go 1.27 or later, to build devc.
- A Debian or Ubuntu base image. Packages are installed with apt.

## Install
```bash
go install ./cmd/devc        # installs devc into $(go env GOPATH)/bin
```

## Quick start
Create `devc.yaml`:
```yaml
name: myproject
features:
  go: {}
  fish: {}
  starship: {}
  zoxide: {}
user: {name: dev, uid: 1000, gid: 1000, shell: /usr/bin/fish, sudo: true}
files:
  .config/fish/config.fish: |
    set -gx EDITOR vim
    zoxide init fish | source
    starship init fish | source
persist: [.cache, go]
run:
  mounts: [~/src/myproject:/workspaces]
```
Then:
```bash
devc up        # builds the image (first time), creates and starts the container
devc shell     # interactive login shell in the container as the user
devc stop
```

[`examples/dev.yaml`](examples/dev.yaml) is a complete definition. It uses every feature with
minimal fish, vim and tmux configs; copy it as a starting point and drop what you don't need.

## Commands
```
devc [-f file.yaml] <command> [flags]
```

| Command | What it does |
|---|---|
| `build [--no-cache]` | Builds the image. |
| `up [--build]` | Makes sure the container is running. It builds the image if it doesn't exist, creates the container if needed, recreates it if the image was rebuilt (volumes are kept), and starts it if it's stopped. `--build` rebuilds the image first. |
| `stop` | Stops the container. |
| `down [--volumes]` | Removes the container. `--volumes` also removes its named volumes. |
| `shell [-- cmd args]` | Opens the user's login shell in the running container. With `-- cmd args`, it runs that command instead. |
| `status` | Shows whether the image is up to date with the YAML, the container's state, and which volumes exist. |
| `logs [-f]` | Shows the container's output. `-f` follows it. |

devc looks for the definition in this order: `-f`, `$DEVC_FILE`, then `devc.yaml` or
`devcontainer.yaml` in the current directory. The definition is checked for errors before any
command runs.

## The definition file
Every field is optional except where noted. Defaults are shown in parentheses.

| Field | Meaning |
|---|---|
| `name` | Short name (`dev`). Used for the image and container defaults. |
| `image` | Image tag to build (`<name>:latest`). |
| `base` | Base image (`ubuntu:24.04`). |
| `platform` | `linux/arm64` or `linux/amd64` (the architecture of the machine running devc). |
| `user` | `name` (`dev`), `uid` (1000), `gid` (1000), `shell` (`/bin/bash`), `sudo` (passwordless sudo, `true`). Use `/usr/bin/fish` as the shell only with the `fish` feature. |
| `timezone` | `TZ`, e.g. `America/New_York`. |
| `env` | Environment variables baked into the image. |
| `certs` | Extra root CA certificates (`.crt`, PEM). They are added to the system trust store, and `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE` and `NODE_EXTRA_CA_CERTS` point at the bundle. Paths are relative to the YAML file. |
| `features` | Software to install. See [Features](#features). |
| `files` | Your config files, written into the user's home. See [Files](#files). |
| `persist` | Directories in the user's home that survive rebuilds. See [Persisted directories](#persisted-directories). |
| `workdir` | Working directory (`/workspaces`). |
| `extra_dockerfile` | Raw Dockerfile instructions, run as root after all features. This is an escape hatch. |
| `run` | How the container runs. See below. |

### Files
`files` maps a path in the user's home to its content. The content is written exactly as
given, owned by the user, before any plugin installs run, so TPM can read your `.tmux.conf`.
devc ships no configs of its own: what's in `files` is what you get.

```yaml
files:
  .config/fish/config.fish: |
    alias ll "eza -la --git"
    fzf --fish | source
  .vimrc: |
    set number expandtab shiftwidth=4
    call mkdir(expand('~/.vim/undo'), 'p')
    set undofile undodir=~/.vim/undo//
  .config/git/config: ""        # an empty file is fine
```
A file inside a `persist` directory only seeds the volume when the volume is first created.
After that, the volume's copy wins, so edit it in the container or recreate the volume.

### `run`
```yaml
run:
  container: dev                    # container name (default: name)
  hostname: dev                     # default: container
  mounts:                           # host:container[:ro]; host may use ~ or be relative to the YAML
    - ~/Projects:/workspaces
  ports: ["3000:3000"]              # as for docker run -p
  env: {FOO: bar}                   # runtime-only environment variables
  ssh_agent: true                   # forward the Docker Desktop / OrbStack SSH agent
  restart: unless-stopped           # no | always | unless-stopped | on-failure
  privileged: false                 # forced on by docker-in-docker
```
Containers also run with `--init`.

### Persisted directories
Each `persist` entry is a path relative to the user's home. It becomes a named volume called
`<container>-<path>`: for example, `.local/share` in container `dev` becomes the
`dev-local-share` volume. With `docker-in-docker`, the inner Docker's data is kept in a
`<container>-docker` volume too. The directories are created in the image owned by the user,
so new volumes start with the right ownership.

Everything outside the persisted directories and the mounts comes from the image and resets
when the container is recreated.

## Features
Features install software. They never write config files: that's what `files` is for.
A few basics are always installed: `ca-certificates`, `curl`, `git`, `gnupg`, `tzdata`,
`less`, `locales`, `man-db`, `openssh-client`, `procps`, `unzip`, `wget` and `zip`. Anything
else that isn't in the catalog goes in `extra_dockerfile`.
Enable a feature by listing it under `features`. `name: {}` uses the defaults:

```yaml
features:
  go: {version: 1.27.3}
  fish: {}
  vim:
    plugins: [tinted-theming/base16-vim, fatih/vim-go@v1.29]
```

| Feature | Installs | Options (default) |
|---|---|---|
| `build-tools` | build-essential and make | |
| `net-tools` | iproute2, iputils-ping, dnsutils (`ip`, `ping`, `dig`) | |
| `ripgrep` | ripgrep (`rg`) | |
| `bat` | bat, available as `bat` | |
| `eza` | eza | |
| `fd` | fd, available as `fd` | |
| `jq` | jq | |
| `boxes` | boxes | |
| `fzf` | fzf from GitHub releases | `version` (0.74.5) |
| `starship` | Starship prompt | `version` (1.27.0) |
| `lazygit` | lazygit | `version` (0.66.2) |
| `lazydocker` | lazydocker | `version` (0.25.2) |
| `glow` | glow | `version` (3.0.0) |
| `workmux` | workmux | `version` (0.1.279) |
| `zoxide` | zoxide | |
| `btop` | btop | |
| `glab` | GitLab CLI, checksum-verified | `version` (1.128.0) |
| `terraform` | HashiCorp Terraform, checksum-verified | `version` (1.17.1) |
| `go` | Go in `/usr/local/go`, `GOPATH=~/go` | `version` (1.27.3) |
| `docker-in-docker` | Docker Engine, buildx and compose. dockerd starts with the container; the user is in the `docker` group | |
| `gcloud` | Google Cloud CLI (gcloud, gsutil, bq) | |
| `fish` | fish shell | |
| `base16-shell` | base16 colour scripts in `~/.config/base16-shell`. Source `profile_helper.fish` (or `.sh`) from your shell config | |
| `vim` | vim + plugins cloned into `~/.vim/pack/bundle/start`. With `go` and the `vim-go` plugin, vim-go's tools are pre-built into `/opt/go-tools/bin` | `plugins` (none) |
| `tmux` | tmux + TPM. The `set -g @plugin` entries in your `~/.tmux.conf` are installed at build time | |
| `claude-code` | Claude Code in `/usr/local/bin`, SHA-256 verified. A rebuild picks up the latest release on the channel | `channel` (stable), `version` (pin) |
| `atlassian-cli` | Atlassian CLI (acli) in `/usr/local/bin`. A rebuild picks up the latest release | |

**vim plugins** are `owner/repo` on GitHub or a full git URL. An optional `@ref` pins a branch
or tag. They are cloned at build time.

## How it works
- **Build:** devc validates the definition, renders a Dockerfile from the enabled features'
  templates and your `files`, and packs it with the certificates into an in-memory tar. It pipes that to
  `docker buildx build --platform … --load -`. The image is labelled with a hash of the build
  context.
- **Up to date or not:** `status` and `up` compare that hash with the current definition, so
  they can tell when the YAML has changed since the last build. `up` recreates the container
  when its image has been rebuilt.
- **Ownership:** containers are labelled with the definition's path. devc refuses to stop or
  remove a container with the same name that it didn't create.
- **Docker-in-Docker:** the image's entrypoint starts dockerd (with cgroup v2 nesting and the
  outer DNS resolver), then runs `sleep infinity`. The container runs privileged with a
  private cgroup namespace. The entrypoint runs as the user and uses sudo for the root steps,
  so docker-in-docker requires `user.sudo`.
- **Default user:** the image's final `USER` is always the configured user, so `docker exec`
  without `--user` (lazydocker, Docker Desktop's Exec tab) doesn't run as root and leave
  root-owned files in persisted volumes such as `~/go`.

## Development
```
cmd/devc/              CLI
internal/config/       YAML schema, defaults, validation, volumes and mounts
internal/features/     feature loading and option handling
  catalog/             one YAML file per feature
  assets/              build-context files (entrypoint.sh)
internal/render/       definition → Dockerfile + build context
internal/docker/       build and container lifecycle through the docker CLI
examples/dev.yaml      full example definition
```

```bash
go test ./...                        # unit tests + golden Dockerfile for examples/dev.yaml
go test ./internal/render -update    # refresh the golden file after an intended change
```

**Adding a feature:** add `internal/features/catalog/<name>.yaml`. The file name is the
feature name, and features render in name order within each stage.

```yaml
description: Go toolchain in /usr/local/go, GOPATH=~/go
apt: [jq]                  # apt packages, installed in the shared apt-get layer
groups: [docker]           # groups the user joins (must exist after the system stage)
dirs: [go]                 # user-owned directories; relative paths are in $HOME
entrypoint: false          # true starts dockerd through assets/entrypoint.sh
options:
  version:
    type: string           # string (default), bool or list
    default: 1.27.3
    help: Release version to install
    pattern: '^[0-9A-Za-z][0-9A-Za-z.+_-]*$'   # restricts string values
system: |                  # root, before the user exists
  RUN curl -fsSL https://go.dev/dl/go{{.Opt.version}}.linux-{{arch "arm64" "amd64"}}.tar.gz | tar -xz -C /usr/local
user: |                    # as the user, after `files` are written
late: |                    # root, at the end, for layers that change often
```

Every key except `description` is optional. Unknown keys, bad patterns and template syntax
errors fail at startup.

Templates see `.Opt` (typed options with defaults), `.User`, `.Home` and `.Cfg`, and can call
these helpers:
- `arch "<arm64 spelling>" "<amd64 spelling>"`, for release-asset names.
- `enabled "<feature>"`.
- `repo "<spec>"` and `hasRepo <list> "<name>"`, for plugin specs.

Features should only install software. Anything a user would want to tune belongs in their
`files`.
