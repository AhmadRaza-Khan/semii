package gen

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProjectFile is the marker file that identifies a semii project.
const ProjectFile = ".semii.yaml"

// TemplateVersion is the template schema version recorded in new projects,
// for `semii upgrade` in v2. The template shape is frozen per version.
const TemplateVersion = 1

// Project is the content of .semii.yaml plus the directory holding it.
type Project struct {
	Root     string   // directory containing .semii.yaml (not stored)
	Semii    string   // framework version that created the project
	Template int      // template schema version
	Module   string   // Go module path
	DB       string   // postgres | mysql | sqlite | mongo
	Auth     []string // session, jwt, apikey; empty for none
	ID       string   // uuid | bigint
}

// Marshal renders .semii.yaml.
func (p Project) Marshal() []byte {
	auth := "[]"
	if len(p.Auth) > 0 {
		auth = "[" + strings.Join(p.Auth, ", ") + "]"
	}
	return fmt.Appendf(nil, `# semii project settings. Generators read this file; keep it in version control.
semii: %s
template: %d
module: %s
db: %s
auth: %s
id: %s
`, p.Semii, p.Template, p.Module, p.DB, auth, p.ID)
}

// FindProject walks up from dir to the nearest .semii.yaml.
func FindProject(dir string) (Project, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Project{}, err
	}
	for {
		path := filepath.Join(dir, ProjectFile)
		if _, err := os.Stat(path); err == nil {
			p, err := ReadProject(path)
			if err != nil {
				return Project{}, err
			}
			p.Root = dir
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Project{}, errors.New("not inside a semii project (no " + ProjectFile + " found); run `semii new <name>` first")
		}
		dir = parent
	}
}

// ReadProject parses a .semii.yaml file. Only the flat key: value subset
// semii writes is supported.
func ReadProject(path string) (Project, error) {
	f, err := os.Open(path)
	if err != nil {
		return Project{}, err
	}
	defer f.Close()
	var p Project
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if i := strings.Index(text, " #"); i >= 0 {
			text = strings.TrimSpace(text[:i])
		}
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, val, ok := strings.Cut(text, ":")
		if !ok {
			return Project{}, fmt.Errorf("%s:%d: expected key: value", path, line)
		}
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		switch strings.TrimSpace(key) {
		case "semii":
			p.Semii = val
		case "template":
			if p.Template, err = strconv.Atoi(val); err != nil {
				return Project{}, fmt.Errorf("%s:%d: template must be an integer", path, line)
			}
		case "module":
			p.Module = val
		case "db":
			p.DB = val
		case "id":
			p.ID = val
		case "auth":
			list := strings.TrimSuffix(strings.TrimPrefix(val, "["), "]")
			for _, a := range strings.Split(list, ",") {
				if a = strings.TrimSpace(a); a != "" && a != "none" {
					p.Auth = append(p.Auth, a)
				}
			}
		default:
			return Project{}, fmt.Errorf("%s:%d: unknown key %q", path, line, key)
		}
	}
	if err := sc.Err(); err != nil {
		return Project{}, err
	}
	return p, p.Validate()
}

// Validate checks the settings.
func (p Project) Validate() error {
	var errs []error
	if p.Module == "" {
		errs = append(errs, errors.New("module is required"))
	}
	switch p.DB {
	case "postgres", "mysql", "sqlite", "mongo":
	default:
		errs = append(errs, fmt.Errorf("db %q must be postgres, mysql, sqlite or mongo", p.DB))
	}
	switch {
	case p.DB == "mongo" && p.ID != "objectid":
		errs = append(errs, fmt.Errorf("id %q: mongo projects use objectid", p.ID))
	case p.DB != "mongo" && p.ID != "uuid" && p.ID != "bigint":
		errs = append(errs, fmt.Errorf("id %q must be uuid or bigint", p.ID))
	}
	for _, a := range p.Auth {
		switch a {
		case "session", "jwt", "apikey":
		default:
			errs = append(errs, fmt.Errorf("auth strategy %q must be session, jwt or apikey", a))
		}
	}
	if p.Template < 1 {
		errs = append(errs, errors.New("template version is missing"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s: %w", ProjectFile, errors.Join(errs...))
	}
	return nil
}

// HasAuth reports whether any auth strategy is configured.
func (p Project) HasAuth() bool { return len(p.Auth) > 0 }
