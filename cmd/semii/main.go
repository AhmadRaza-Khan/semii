// Command semii scaffolds and runs Go API projects. See `semii help`.
package main

import (
	"os"

	"github.com/AhmadRaza-Khan/semii/internal/cli"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		os.Stderr.WriteString("semii: " + err.Error() + "\n")
		os.Exit(cli.ExitInternal)
	}
	os.Exit(cli.Run(os.Args[1:], &cli.Env{
		Stdout: os.Stdout, Stderr: os.Stderr, Dir: dir, Getenv: os.Getenv,
	}))
}
