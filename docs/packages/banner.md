# banner

Prints the semii leaf in a terminal, drawn with half-block characters (two
pixels per cell) in true colour or the 256-colour palette.

```go
banner.Print(os.Stderr, "shop · development", "built with semii")
```

`Print` writes nothing unless the writer is an interactive terminal with
colour enabled, so pipes, log files and CI output never contain it:

| Environment | Result |
|-------------|--------|
| `NO_COLOR` set, or `TERM=dumb` | nothing |
| `COLORTERM=truecolor` or `24bit`, or Windows Terminal | 24-bit colour |
| anything else | 256 colours |

`DetectMode` and `Render` expose the same logic for callers that manage
their own output.

The `semii` CLI shows the leaf on `semii`, `semii help`, `semii version`
and `semii new` (disable it with `--no-color`). Generated apps show it on
startup when `SEMII_ENV=development`.

## Logo files

`docs/assets/` holds the logo: `logo.png` (transparent, 800 px),
`logo.svg` (the same image as SVG), `icon.png` (512×512) and
`social-preview.png` (1280×640, for the GitHub repository's social
preview).
