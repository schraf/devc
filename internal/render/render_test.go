package render

import (
	"archive/tar"
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/schraf/devcontainer/internal/config"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestDockerfileGolden(t *testing.T) {
	cfg, err := config.Load("../../examples/dev.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Dockerfile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "dev.Dockerfile")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("Dockerfile differs from %s; run go test ./internal/render -update and review the diff", golden)
	}
}

func TestContext(t *testing.T) {
	cfg, err := config.Load("../../examples/dev.yaml")
	if err != nil {
		t.Fatal(err)
	}
	data, err := Context(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	sort.Strings(names)
	want := "Dockerfile entrypoint.sh"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("context = %s, want %s", got, want)
	}
}

func TestCerts(t *testing.T) {
	crt := filepath.Join(t.TempDir(), "corp.crt")
	if err := os.WriteFile(crt, []byte("-----BEGIN CERTIFICATE-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.New("t")
	cfg.Certs = []string{crt}
	df, err := Dockerfile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"COPY certs/ /usr/local/share/ca-certificates/extra/", "SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt"} {
		if !strings.Contains(df, want) {
			t.Errorf("missing %q in:\n%s", want, df)
		}
	}
	data, err := Context(cfg)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(data))
	found := false
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		found = found || h.Name == "certs/corp.crt"
	}
	if !found {
		t.Error("certs/corp.crt missing from the build context")
	}
}

func TestMinimal(t *testing.T) {
	cfg := config.New("tiny")
	cfg.Features["go"] = nil
	got, err := Dockerfile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"FROM ubuntu:24.04", "go1.27.3.linux-", `CMD ["sleep", "infinity"]`, "USER dev\nCMD"} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in:\n%s", s, got)
		}
	}
	if strings.Contains(got, "ENTRYPOINT") || strings.Contains(got, "COPY certs/") {
		t.Errorf("unexpected entrypoint/certs in:\n%s", got)
	}
}

func TestValidationErrors(t *testing.T) {
	for name, yaml := range map[string]string{
		"unknown feature": "features: {nope: {}}",
		"unknown option":  "features: {go: {verison: 1}}",
		"bad version":     "features: {go: {version: '1; rm -rf /'}}",
		"fish shell":      "user: {name: dev, uid: 1000, gid: 1000, shell: /usr/bin/fish}",
		"bad plugin":      "features: {vim: {plugins: [justaname]}}",
		"wrong type":      "features: {fish: {aliases: [a, b]}}",
		"missing cert":    "certs: [nope.crt]",
	} {
		cfg, err := config.Parse([]byte(yaml))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		if _, err := Dockerfile(cfg); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestFiles(t *testing.T) {
	cfg := config.New("t")
	cfg.Features["tmux"] = nil
	cfg.Files = map[string]string{".tmux.conf": "set -g mouse on", ".config/git/config": "", ".config/x/y": "$HOME stays literal\n"}
	got, err := Dockerfile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"RUN mkdir -p /home/dev/.config/git /home/dev/.config/x\n",
		"COPY --chown=1000:1000 <<'DEVC_EOF' /home/dev/.tmux.conf\nset -g mouse on\nDEVC_EOF\n",
		"COPY --chown=1000:1000 <<'DEVC_EOF' /home/dev/.config/git/config\nDEVC_EOF\n",
		"$HOME stays literal\nDEVC_EOF",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Files are written before user-stage features so TPM can read ~/.tmux.conf.
	if strings.Index(got, "/home/dev/.tmux.conf") > strings.Index(got, "tpm/bin/install_plugins") {
		t.Error("files must come before the tmux plugin install")
	}
	cfg.Files = map[string]string{".x": "a\nDEVC_EOF\nb"}
	if _, err := Dockerfile(cfg); err == nil {
		t.Error("expected an error for content containing the heredoc delimiter")
	}
}
