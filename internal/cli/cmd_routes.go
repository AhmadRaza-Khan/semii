package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/AhmadRaza-Khan/semii/internal/gen"
	"github.com/AhmadRaza-Khan/semii/web"
)

func routesMain(p gen.Project) string {
	return strings.NewReplacer("MODULE", p.Module).Replace(`package main

import (
	"encoding/json"
	"os"

	"MODULE/internal/app"
)

func main() {
	routes, err := app.Routes()
	out := struct {
		Routes any    ` + "`json:\"routes\"`" + `
		Error  string ` + "`json:\"error,omitempty\"`" + `
	}{Routes: routes}
	if err != nil {
		out.Error = err.Error()
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
`)
}

func runRoutes(e *Env, args []string) error {
	fs := flag.NewFlagSet("routes", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	p, err := e.project()
	if err != nil {
		return err
	}
	raw, err := e.goRun(p.Root, "routes", routesMain(p))
	if err != nil {
		return err
	}
	var res struct {
		Routes []web.RouteInfo `json:"routes"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("decode routes: %w", err)
	}
	if *asJSON {
		b, _ := json.MarshalIndent(res.Routes, "", "  ")
		e.printf("%s\n", b)
	} else {
		tw := tabwriter.NewWriter(e.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "METHOD\tPATH\tPOLICY\tHANDLER")
		for _, r := range res.Routes {
			method, policy := r.Method, r.Policy
			if method == "" {
				method = "*"
			}
			if policy == "" {
				policy = "NONE"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", method, r.Path, policy, r.Handler)
		}
		_ = tw.Flush()
	}
	if res.Error != "" {
		return userErr("%s", res.Error)
	}
	return nil
}

func runVersion(e *Env, args []string) error {
	e.printf("semii %s (%s)\n", Version, runtime.Version())
	if p, err := gen.FindProject(e.Dir); err == nil {
		e.printf("project %s: created by semii %s, template v%d\n", p.Module, p.Semii, p.Template)
	}
	return nil
}
