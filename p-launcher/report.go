package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// reportOpts are the `report` flags. Range picks which file --open shows;
// all three ranges are always written so the header toggle links resolve.
type reportOpts struct {
	demo, open bool
	rng, theme string
	out        string // directory
}

var reportRanges = map[string]int{"7d": 7, "30d": 30, "90d": 90}

func parseReportFlags(args []string, stderr io.Writer) (reportOpts, error) {
	var o reportOpts
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&o.demo, "demo", false, "render the seeded demo dataset instead of the database")
	fs.BoolVar(&o.open, "open", false, "open the rendered file with xdg-open")
	fs.StringVar(&o.rng, "range", "30d", "7d, 30d or 90d: the file --open shows (all three are written)")
	fs.StringVar(&o.theme, "theme", "dark", "dark or light")
	fs.StringVar(&o.out, "out", "", "output directory (default: the p-launcher data dir)")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() != 0 {
		return o, fmt.Errorf("report: unexpected argument %q", fs.Arg(0))
	}
	if _, ok := reportRanges[o.rng]; !ok {
		return o, fmt.Errorf("report: --range must be 7d, 30d or 90d")
	}
	if _, ok := themes[o.theme]; !ok {
		return o, fmt.Errorf("report: --theme must be dark or light")
	}
	return o, nil
}

// runReport renders report-7d.html, report-30d.html and report-90d.html into
// the out dir and prints the path of the --range file. s is nil with --demo.
func runReport(o reportOpts, s *Store, dataDir string, stdout io.Writer) error {
	if o.out == "" {
		o.out = dataDir
	}
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return err
	}
	now := time.Now()
	theme := themes[o.theme]
	var raw rawData
	if !o.demo {
		var err error
		raw, err = loadReportData(s, now.AddDate(0, 0, -90), now)
		if err != nil {
			return err
		}
	}
	var chosen string
	for rng, days := range reportRanges {
		started := time.Now()
		var r Report
		if o.demo {
			r = demoReport(now, theme)
			r.Range = rng
		} else {
			r = computeReport(raw, rng, now.AddDate(0, 0, -days), now, theme)
		}
		r.RenderMillis = time.Since(started).Milliseconds()
		path := filepath.Join(o.out, "report-"+rng+".html")
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		if err := renderReport(f, r); err != nil {
			f.Close()
			return fmt.Errorf("render %s: %w", path, err)
		}
		if err := f.Close(); err != nil {
			return err
		}
		if rng == o.rng {
			chosen = path
		}
	}
	fmt.Fprintln(stdout, chosen)
	if o.open {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := runCmd(ctx, "xdg-open", chosen); err != nil {
			return err
		}
	}
	return nil
}
