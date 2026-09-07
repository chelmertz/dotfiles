package main

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
)

// iconPaths are Lucide-style 24-unit stroke icons, one per state. The same
// pictures appear in rofi (as files) and in the report (inline). Design
// source of truth: report-design/gen.mjs ICON_PATHS.
var iconPaths = map[string]string{
	"you":      `<path d="M18 11V6a2 2 0 0 0-4 0v1"></path><path d="M14 10V4a2 2 0 0 0-4 0v2"></path><path d="M10 10.5V6a2 2 0 0 0-4 0v8"></path><path d="M18 8a2 2 0 1 1 4 0v6a8 8 0 0 1-8 8h-2c-2.8 0-4.5-.86-5.99-2.34l-3.6-3.6a2 2 0 0 1 2.83-2.82L7 15"></path>`,
	"claude":   `<path d="M12 8V4H8"></path><rect x="4" y="8" width="16" height="12" rx="2"></rect><path d="M2 14h2"></path><path d="M20 14h2"></path><path d="M15 13v2"></path><path d="M9 13v2"></path>`,
	"idle":     `<path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"></path>`,
	"review":   `<path d="M7.9 20A9 9 0 1 0 4 16.1L2 22Z"></path><path d="M12 8v4"></path><path d="M12 16h.01"></path>`,
	"archived": `<rect x="2" y="3" width="20" height="5" rx="1"></rect><path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8"></path><path d="M10 12h4"></path>`,
	"reopened": `<path d="M21 12a9 9 0 1 1-2.64-6.36"></path><path d="M21 3v6h-6"></path>`,
}

// iconSVG renders one icon inline. Unknown names render nothing.
func iconSVG(name, color string, size int) template.HTML {
	p, ok := iconPaths[name]
	if !ok {
		return ""
	}
	return template.HTML(fmt.Sprintf(`<svg width="%d" height="%d" viewBox="0 0 24 24" fill="none" stroke="%s" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" style="display:block;flex:none">%s</svg>`, size, size, color, p))
}

// rofiIconColors are the dark theme accents (rofi's theme is dark). idle is
// lighter than the report's idle gray so it stays visible on rofi's rows.
var rofiIconColors = map[string]string{"you": "#d95926", "claude": "#3987e5", "idle": "#8a9199", "review": "#d95926", "archived": "#8a9199"}

// writeIcons writes the state icons plus a transparent blank as standalone
// SVG files for rofi's per-row icons (librsvg needs the xmlns), rewriting a
// file only when its content differs. Returns name → absolute path.
func writeIcons(dir string) (map[string]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	files := map[string][]byte{"blank": []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"></svg>`)}
	for name, color := range rofiIconColors {
		files[name] = []byte(`<svg xmlns="http://www.w3.org/2000/svg" ` + string(iconSVG(name, color, 24))[len("<svg "):])
	}
	out := make(map[string]string, len(files))
	for name, b := range files {
		p := filepath.Join(dir, name+".svg")
		if old, err := os.ReadFile(p); err != nil || !bytes.Equal(old, b) {
			if err := os.WriteFile(p, b, 0o644); err != nil {
				return nil, err
			}
		}
		out[name] = p
	}
	return out, nil
}
