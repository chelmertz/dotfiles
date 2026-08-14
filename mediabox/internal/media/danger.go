package media

import "bytes"

// The danger gate. It runs before any probe and looks only at the file's own
// leading bytes — not at what the file contains. A PS1 disc image is full of
// executables; that is its payload, not its signature.

// execSigs are leading byte sequences that mean "this is code".
var execSigs = []struct {
	magic []byte
	what  string
}{
	{[]byte{0x7F, 'E', 'L', 'F'}, "an ELF binary"},
	{[]byte{'M', 'Z'}, "a DOS/PE executable"},
	{[]byte{0xFE, 0xED, 0xFA, 0xCE}, "a Mach-O binary"},
	{[]byte{0xFE, 0xED, 0xFA, 0xCF}, "a Mach-O binary"},
	{[]byte{0xCF, 0xFA, 0xED, 0xFE}, "a Mach-O binary"},
	{[]byte{0xCA, 0xFE, 0xBA, 0xBE}, "a Java class or fat Mach-O binary"},
	{[]byte{0xD0, 0xCF, 0x11, 0xE0}, "an OLE compound file (MSI/Office)"},
	{[]byte{'#', '!'}, "a script with a shebang"},
	{[]byte{'<', '?', 'p', 'h', 'p'}, "PHP source"},
	{[]byte{0x4C, 0x00, 0x00, 0x00, 0x01, 0x14, 0x02, 0x00}, "a Windows shortcut"},
}

// execExts are refused whatever the bytes say. Reading the *last* extension is
// what defeats the "Arrival.mkv.exe" trick.
var execExts = map[string]string{
	"exe": "Windows executable", "dll": "Windows library",
	"com": "DOS executable", "scr": "Windows screensaver",
	"pif": "Windows shortcut", "msi": "Windows installer",
	"bat": "batch script", "cmd": "batch script",
	"ps1": "PowerShell script", "vbs": "VBScript",
	"sh": "shell script", "bash": "shell script", "zsh": "shell script",
	"js": "JavaScript", "jse": "JavaScript", "wsf": "Windows script",
	"jar": "Java archive", "so": "shared library", "dylib": "shared library",
	"app": "macOS application", "deb": "Debian package", "rpm": "RPM package",
	"appimage": "AppImage", "run": "installer",
	"desktop": "desktop entry", "lnk": "Windows shortcut",
	"py": "Python script", "pl": "Perl script", "rb": "Ruby script",
}

func dangerGate(ext string, head []byte) error {
	if what, bad := execExts[ext]; bad {
		return reject("refused at the door: .%s is a %s", ext, what)
	}
	for _, s := range execSigs {
		if bytes.HasPrefix(head, s.magic) {
			return reject("refused at the door: content is %s", s.what)
		}
	}
	return nil
}
