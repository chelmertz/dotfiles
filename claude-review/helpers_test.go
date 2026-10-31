package main

import "os"

const (
	owner       = "acme"
	repoName    = "widgets"
	originURL   = "git@github.com:acme/widgets.git"
	claudeBin   = "claude"
	resume      = "--resume"
	sh          = "/bin/sh"
	slug        = "acme/widgets"
	httpsURL    = "https://github.com/acme/widgets"
	codeRoot    = "/h/code"
	ghostty     = "ghostty"
	upstream    = "upstream"
	originName  = "origin"
	minimalPath = "PATH=/bin:/usr/bin"
	toFlag      = "--to"
	detachedCfg = `{"terminal": false}`
)

// origin builds the usual single-remote map.
func origin(url string) map[string]string { return map[string]string{originName: url} }

var widgetsPR = PR{Owner: owner, Repo: repoName, Number: 42}

func writeFile(p, body string) error {
	return os.WriteFile(p, []byte(body), 0o700) //nolint:gosec // test helper writes executable stubs
}
