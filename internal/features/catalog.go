// Package features is the catalog of things a dev container can include. Each feature is one
// embedded catalog/<name>.yaml file: metadata, typed options and Dockerfile snippets.
package features

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

//go:embed catalog assets
var files embed.FS

// Stage is where in the Dockerfile a snippet goes.
type Stage int

const (
	// StageSystem runs as root before the user exists: toolchains and CLIs.
	StageSystem Stage = iota
	// StageUser runs as the user after it's created: config files and plugins.
	StageUser
	// StageLate runs as root at the end, for layers that change often (e.g. Claude Code).
	StageLate
)

// Kind is an option's value type.
type Kind int

const (
	String Kind = iota
	Bool
	List
)

func (k Kind) String() string {
	return [...]string{"string", "bool", "list"}[k]
}

var kinds = map[string]Kind{"": String, "string": String, "bool": Bool, "list": List}

// Option is one setting a feature accepts.
type Option struct {
	Key     string
	Kind    Kind
	Default any
	Help    string
	// Pattern, when set, restricts a String value (they are pasted into shell commands).
	Pattern *regexp.Regexp
}

// Feature is one catalog entry.
type Feature struct {
	Name        string
	Description string
	Options     []Option
	// Apt packages installed in the image's single apt-get layer.
	Apt []string
	// Groups the user is added to (they must exist after the system stage).
	Groups []string
	// Dirs are created owned by the user before the user stage. Relative paths are in $HOME.
	Dirs []string
	// Entrypoint makes the image start dockerd through the embedded entrypoint.sh.
	Entrypoint bool

	templates map[Stage]string
}

// Option looks up an option by key.
func (f *Feature) Option(key string) (Option, bool) {
	for _, o := range f.Options {
		if o.Key == key {
			return o, true
		}
	}
	return Option{}, false
}

// HasStage reports whether the feature contributes a snippet to stage s.
func (f *Feature) HasStage(s Stage) bool {
	_, ok := f.templates[s]
	return ok
}

// spec is the YAML form of a catalog file.
type spec struct {
	Description string
	Apt         []string
	Groups      []string
	Dirs        []string
	Entrypoint  bool
	Options     map[string]struct {
		Type    string
		Default any
		Help    string
		Pattern string
	}
	// Dockerfile snippets (text/template) for each stage.
	System, User, Late string
}

// catalog is sorted by name, which is also the render order within each stage. Features
// install software; the user's own config files belong in the definition's `files`.
var catalog []*Feature

var byName = map[string]*Feature{}

func init() {
	entries, err := fs.ReadDir(files, "catalog")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".yaml")
		if !ok {
			panic("features: catalog/" + e.Name() + " is not a .yaml file")
		}
		data, _ := files.ReadFile("catalog/" + e.Name())
		f, err := parse(name, data)
		if err != nil {
			panic(fmt.Sprintf("features: catalog/%s: %v", e.Name(), err))
		}
		catalog = append(catalog, f)
		byName[name] = f
	}
}

// parse builds a Feature from its catalog file, checking option types, patterns and template
// syntax so mistakes fail at startup rather than mid-render.
func parse(name string, data []byte) (*Feature, error) {
	var s spec
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	f := &Feature{Name: name, Description: s.Description, Apt: s.Apt, Groups: s.Groups, Dirs: s.Dirs,
		Entrypoint: s.Entrypoint, templates: map[Stage]string{}}
	for key, o := range s.Options {
		kind, ok := kinds[o.Type]
		if !ok {
			return nil, fmt.Errorf("option %s: unknown type %q", key, o.Type)
		}
		opt := Option{Key: key, Kind: kind, Default: o.Default, Help: o.Help}
		if o.Pattern != "" {
			re, err := regexp.Compile(o.Pattern)
			if err != nil {
				return nil, fmt.Errorf("option %s: %w", key, err)
			}
			opt.Pattern = re
		}
		f.Options = append(f.Options, opt)
	}
	sort.Slice(f.Options, func(i, j int) bool { return f.Options[i].Key < f.Options[j].Key })
	for stage, src := range map[Stage]string{StageSystem: s.System, StageUser: s.User, StageLate: s.Late} {
		if src == "" {
			continue
		}
		if _, err := template.New(name).Funcs(funcMap(nil)).Parse(src); err != nil {
			return nil, err
		}
		f.templates[stage] = src
	}
	return f, nil
}

// All returns the catalog in render order.
func All() []*Feature { return catalog }

// Get returns a feature by name.
func Get(name string) (*Feature, bool) {
	f, ok := byName[name]
	return f, ok
}

// Names returns all feature names, sorted.
func Names() []string {
	names := make([]string, 0, len(catalog))
	for _, f := range catalog {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names
}

// Asset returns an embedded build-context file (e.g. entrypoint.sh).
func Asset(name string) []byte {
	data, err := files.ReadFile(path.Join("assets", name))
	if err != nil {
		panic(fmt.Sprintf("features: missing asset %s", name))
	}
	return data
}
