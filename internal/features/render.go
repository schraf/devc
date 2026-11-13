package features

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"text/template"

	"github.com/schraf/devcontainer/internal/config"
)

// Data is what feature templates see.
type Data struct {
	Cfg  *config.Config
	User config.User
	Home string
	Opt  map[string]any // the feature's options, typed and with defaults applied
}

// Values returns a feature's options for cfg, coerced to their declared kinds with defaults
// filled in.
func (f *Feature) Values(cfg *config.Config) (map[string]any, error) {
	raw := cfg.Features[f.Name]
	out := map[string]any{}
	var errs []error
	for key := range raw {
		if _, ok := f.Option(key); !ok {
			errs = append(errs, fmt.Errorf("%s: unknown option %q", f.Name, key))
		}
	}
	for _, o := range f.Options {
		v, set := raw[o.Key]
		if !set || v == nil {
			v = o.Default
		}
		c, err := coerce(o.Kind, v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s.%s: %w", f.Name, o.Key, err))
			c, _ = coerce(o.Kind, nil) // keep the value typed so callers can still read it
		} else if s, ok := c.(string); ok && o.Pattern != nil && !o.Pattern.MatchString(s) {
			errs = append(errs, fmt.Errorf("%s.%s: %q is not a valid value", f.Name, o.Key, s))
		}
		out[o.Key] = c
	}
	return out, errors.Join(errs...)
}

func coerce(k Kind, v any) (any, error) {
	switch k {
	case String:
		switch v := v.(type) {
		case nil:
			return "", nil
		case string:
			return v, nil
		case int, float64, bool:
			return fmt.Sprint(v), nil
		}
	case Bool:
		switch v := v.(type) {
		case nil:
			return false, nil
		case bool:
			return v, nil
		}
	case List:
		switch v := v.(type) {
		case nil:
			return []string{}, nil
		case []string:
			return v, nil
		case []any:
			out := make([]string, 0, len(v))
			for _, item := range v {
				switch item.(type) {
				case string, int, float64, bool:
					out = append(out, fmt.Sprint(item))
				default:
					return nil, fmt.Errorf("list items must be scalars, got %T", item)
				}
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("expected a %s, got %T", k, v)
}

// Validate checks cfg against the catalog: known features, valid options and cross-feature
// requirements. It includes config.Validate.
func Validate(cfg *config.Config) error {
	errs := []error{cfg.Validate()}
	for name := range cfg.Features {
		f, ok := Get(name)
		if !ok {
			errs = append(errs, fmt.Errorf("unknown feature %q (known: %s)", name, strings.Join(Names(), ", ")))
			continue
		}
		if _, err := f.Values(cfg); err != nil {
			errs = append(errs, err)
		}
	}
	for _, f := range All() {
		if f.Entrypoint && cfg.Enabled(f.Name) && !cfg.User.Sudo {
			errs = append(errs, fmt.Errorf("the %s feature needs user.sudo: its entrypoint uses sudo to start as root", f.Name))
		}
	}
	if strings.HasSuffix(cfg.User.Shell, "/fish") && !cfg.Enabled("fish") {
		errs = append(errs, errors.New("user.shell is fish but the fish feature is not enabled"))
	}
	for _, p := range pluginList(cfg, "vim") {
		if _, err := ParseRepo(p); err != nil {
			errs = append(errs, fmt.Errorf("vim.plugins: %w", err))
		}
	}
	seen := map[string]string{}
	for _, c := range cfg.Certs {
		base := path.Base(c)
		if prev, dup := seen[base]; dup {
			errs = append(errs, fmt.Errorf("certs %s and %s have the same file name", prev, c))
		}
		seen[base] = c
	}
	return errors.Join(errs...)
}

func pluginList(cfg *config.Config, feature string) []string {
	f, _ := Get(feature)
	if !cfg.Enabled(feature) {
		return nil
	}
	vals, err := f.Values(cfg)
	if err != nil {
		return nil
	}
	return vals["plugins"].([]string)
}

func hasRepo(list []string, name string) bool {
	for _, spec := range list {
		if r, err := ParseRepo(spec); err == nil && r.Name == name {
			return true
		}
	}
	return false
}

// Repo is a git repository reference: owner/repo[@ref] on GitHub or a full URL[@ref].
type Repo struct {
	URL  string
	Name string // directory name to clone into
	Ref  string // optional branch or tag
}

// ParseRepo parses a plugin spec.
func ParseRepo(spec string) (Repo, error) {
	spec = strings.TrimSpace(spec)
	var r Repo
	// A ref is whatever follows the last @, unless the @ is part of an ssh URL (git@host:…).
	if i := strings.LastIndex(spec, "@"); i > 0 && !strings.Contains(spec[i:], ":") {
		spec, r.Ref = spec[:i], spec[i+1:]
	}
	switch {
	case strings.Contains(spec, "://") || strings.HasPrefix(spec, "git@"):
		r.URL = spec
	case strings.Count(spec, "/") == 1 && !strings.HasPrefix(spec, "/") && !strings.HasSuffix(spec, "/"):
		r.URL = "https://github.com/" + spec
	default:
		return r, fmt.Errorf("%q: expected owner/repo[@ref] or a git URL", spec)
	}
	r.Name = strings.TrimSuffix(path.Base(r.URL), ".git")
	if strings.ContainsAny(r.URL+r.Ref, " '\"\\$`") {
		return r, fmt.Errorf("%q: contains characters that aren't allowed", spec)
	}
	return r, nil
}

// Render executes the feature's snippet for one stage. It returns "" when the feature has
// nothing for that stage.
func (f *Feature) Render(cfg *config.Config, stage Stage) (string, error) {
	src, ok := f.templates[stage]
	if !ok {
		return "", nil
	}
	opts, err := f.Values(cfg)
	if err != nil {
		return "", err
	}
	d := Data{Cfg: cfg, User: cfg.User, Home: "/home/" + cfg.User.Name, Opt: opts}
	return execute(f.Name, src, d)
}

// funcMap is the helpers templates can call. cfg may be nil when only parsing.
func funcMap(cfg *config.Config) template.FuncMap {
	return template.FuncMap{
		// arch picks the release-asset spelling for the target platform.
		"arch": func(arm64, amd64 string) string {
			if cfg.Arch() == "amd64" {
				return amd64
			}
			return arm64
		},
		"enabled": func(name string) bool { return cfg.Enabled(name) },
		"repo":    func(spec string) (Repo, error) { return ParseRepo(spec) },
		"hasRepo": hasRepo,
		"trim":    strings.TrimSpace,
		"join":    strings.Join,
	}
}

func execute(name, src string, d Data) (string, error) {
	t, err := template.New(name).Funcs(funcMap(d.Cfg)).Option("missingkey=error").Parse(src)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, d); err != nil {
		return "", err
	}
	return b.String(), nil
}
