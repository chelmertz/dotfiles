package media

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
)

// Disc images are where guessing by extension hurts most: .bin, .iso and .img
// are used by every console here. Rather than ask the user, read the disc's own
// filesystem and find out which machine it was pressed for.

// sectorLayout maps a logical block number to a byte offset. A plain .iso holds
// 2048-byte sectors back to back; a raw .bin keeps the CD's 2352-byte sectors
// with the user data set in at a fixed offset.
type sectorLayout struct {
	size   int64
	offset int64
	name   string
}

var sectorLayouts = []sectorLayout{
	{2048, 0, "2048-byte sectors"},
	{2352, 16, "raw MODE1/2352 sectors"},
	{2352, 24, "raw MODE2/2352 sectors"},
}

const (
	isoPVDBlock  = 16   // the primary volume descriptor always lives here
	isoBlockData = 2048 // user bytes per sector, whatever the physical size
)

func (l sectorLayout) readBlock(r io.ReaderAt, lba int64) ([]byte, error) {
	buf := make([]byte, isoBlockData)
	_, err := r.ReadAt(buf, lba*l.size+l.offset)
	return buf, err
}

// readExtent pulls n bytes of file data starting at a logical block, hopping
// the physical sector grid as it goes.
func (l sectorLayout) readExtent(r io.ReaderAt, lba int64, n int64) ([]byte, error) {
	var out []byte
	for n > 0 {
		b, err := l.readBlock(r, lba)
		if err != nil {
			return out, err
		}
		if n < isoBlockData {
			b = b[:n]
		}
		out = append(out, b...)
		n -= isoBlockData
		lba++
	}
	return out, nil
}

// isoEntry is one name in a directory.
type isoEntry struct {
	name  string
	lba   int64
	size  int64
	isDir bool
}

// openISO finds a layout whose primary volume descriptor is where it should be.
func openISO(r io.ReaderAt) (sectorLayout, bool) {
	for _, l := range sectorLayouts {
		b, err := l.readBlock(r, isoPVDBlock)
		if err != nil || len(b) < 6 {
			continue
		}
		if b[0] == 1 && bytes.Equal(b[1:6], []byte("CD001")) {
			return l, true
		}
	}
	return sectorLayout{}, false
}

// rootEntries lists the root directory of an ISO9660 volume.
func rootEntries(r io.ReaderAt, l sectorLayout) ([]isoEntry, bool) {
	pvd, err := l.readBlock(r, isoPVDBlock)
	if err != nil || len(pvd) < 190 {
		return nil, false
	}
	// The root directory record is embedded in the descriptor at offset 156.
	rec := pvd[156:190]
	lba := int64(binary.LittleEndian.Uint32(rec[2:6]))
	size := int64(binary.LittleEndian.Uint32(rec[10:14]))
	if lba <= 0 || size <= 0 || size > 4<<20 {
		return nil, false
	}
	data, err := l.readExtent(r, lba, size)
	if err != nil && len(data) == 0 {
		return nil, false
	}
	return parseDir(data), true
}

// parseDir walks the directory records in one extent. A zero length byte means
// the rest of this sector is padding, so skip to the next sector boundary.
func parseDir(data []byte) []isoEntry {
	var out []isoEntry
	for i := 0; i < len(data); {
		recLen := int(data[i])
		if recLen == 0 {
			next := (i/isoBlockData + 1) * isoBlockData
			if next <= i {
				break
			}
			i = next
			continue
		}
		if i+recLen > len(data) || recLen < 33 {
			break
		}
		rec := data[i : i+recLen]
		nameLen := int(rec[32])
		if 33+nameLen <= len(rec) && nameLen > 0 {
			name := string(rec[33 : 33+nameLen])
			// Strip the ";1" version suffix ISO9660 appends to file names.
			if k := strings.IndexByte(name, ';'); k >= 0 {
				name = name[:k]
			}
			if name != "\x00" && name != "\x01" { // "." and ".."
				out = append(out, isoEntry{
					name:  strings.ToUpper(name),
					lba:   int64(binary.LittleEndian.Uint32(rec[2:6])),
					size:  int64(binary.LittleEndian.Uint32(rec[10:14])),
					isDir: rec[25]&0x02 != 0,
				})
			}
		}
		i += recLen
	}
	return out
}

func probeDisc(r io.ReaderAt, head []byte, _ int64) (ID, bool) {
	// A Dreamcast image announces itself in the boot header, well before any
	// filesystem parsing is needed.
	if bytes.Contains(head, []byte("SEGA SEGAKATANA")) {
		return ID{System: "dreamcast", Kind: KindDisc, Detail: "Dreamcast boot header (SEGA SEGAKATANA)"}, true
	}

	layout, ok := openISO(r)
	if !ok {
		return ID{}, false
	}
	entries, ok := rootEntries(r, layout)
	if !ok {
		return ID{System: "", Kind: KindDisc}, false
	}

	names := make(map[string]isoEntry, len(entries))
	for _, e := range entries {
		names[e.name] = e
	}

	// PSP puts a UMD data file and a PSP_GAME directory at the root; neither
	// appears on a PlayStation disc.
	if _, ok := names["UMD_DATA.BIN"]; ok {
		return ID{System: "psp", Kind: KindDisc, Detail: "ISO9660 with UMD_DATA.BIN (" + layout.name + ")"}, true
	}
	if e, ok := names["PSP_GAME"]; ok && e.isDir {
		return ID{System: "psp", Kind: KindDisc, Detail: "ISO9660 with PSP_GAME/ (" + layout.name + ")"}, true
	}

	// Both PS1 and PSP carry SYSTEM.CNF, but they spell the boot key
	// differently: BOOT= for the PlayStation, BOOT2= for the PSP.
	if e, ok := names["SYSTEM.CNF"]; ok && e.size > 0 && e.size < 64<<10 {
		if cnf, err := layout.readExtent(r, e.lba, e.size); err == nil {
			if id, ok := classifySystemCNF(string(cnf), layout.name); ok {
				return id, true
			}
		}
	}
	if _, ok := names["PSX.EXE"]; ok {
		return ID{System: "ps1", Kind: KindDisc, Detail: "ISO9660 with PSX.EXE (" + layout.name + ")"}, true
	}
	if _, ok := names["IP.BIN"]; ok {
		return ID{System: "dreamcast", Kind: KindDisc, Detail: "ISO9660 with IP.BIN (" + layout.name + ")"}, true
	}

	return ID{}, false
}

// ps1BootIDs are the regional prefixes a PlayStation boot executable uses.
var ps1BootIDs = []string{"SLUS", "SLES", "SCUS", "SCES", "SLPS", "SLPM", "SCPS", "SLED", "SCED"}

func classifySystemCNF(cnf, layoutName string) (ID, bool) {
	upper := strings.ToUpper(cnf)
	if strings.Contains(upper, "BOOT2") {
		return ID{System: "psp", Kind: KindDisc, Detail: "ISO9660, SYSTEM.CNF BOOT2 (" + layoutName + ")"}, true
	}
	if strings.Contains(upper, "BOOT") {
		for _, id := range ps1BootIDs {
			if strings.Contains(upper, id) {
				return ID{System: "ps1", Kind: KindDisc, Detail: "ISO9660, SYSTEM.CNF BOOT=" + id + " (" + layoutName + ")"}, true
			}
		}
		// A boot line with an unfamiliar executable name is still a
		// PlayStation-shaped disc, but say so less confidently.
		return ID{System: "ps1", Kind: KindDisc, Detail: "ISO9660, SYSTEM.CNF BOOT (" + layoutName + ")"}, true
	}
	return ID{}, false
}
