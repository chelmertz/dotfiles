package media

import (
	"encoding/binary"
	"strings"
)

// Fixtures are built rather than checked in: a few dozen bytes of header is the
// whole of what the probes look at, and generating them keeps the intent of
// each test visible next to the assertion.

func pad(b []byte, n int) []byte {
	if len(b) >= n {
		return b
	}
	return append(b, make([]byte, n-len(b))...)
}

func at(size int, parts map[int][]byte) []byte {
	b := make([]byte, size)
	for off, v := range parts {
		copy(b[off:], v)
	}
	return b
}

func nesROM() []byte {
	return append([]byte("NES\x1a\x02\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"),
		make([]byte, 40*1024)...)
}

func n64ROM(magic uint32) []byte {
	b := make([]byte, 4*1024*1024)
	binary.BigEndian.PutUint32(b[:4], magic)
	copy(b[0x20:], "SUPER MARIO 64")
	return b
}

func gbROM(colour bool) []byte {
	b := at(64*1024, map[int][]byte{0x104: gbLogo})
	copy(b[0x134:], "TETRIS")
	if colour {
		b[0x143] = 0xC0
	}
	return b
}

func gbaROM() []byte {
	b := at(1024*1024, map[int][]byte{0x04: gbaLogoPrefix})
	b[0xB2] = 0x96
	copy(b[0xA0:], "METROID4")
	return b
}

func genesisROM() []byte {
	return at(512*1024, map[int][]byte{0x100: []byte("SEGA MEGA DRIVE ")})
}

func smsROM() []byte {
	return at(256*1024, map[int][]byte{0x7FF0: []byte("TMR SEGA")})
}

// snesROM writes a LoROM header whose checksum and complement satisfy the
// identity the probe checks. Passing valid=false breaks exactly that identity
// and nothing else, which is what makes it a useful negative case.
func snesROM(valid bool) []byte {
	b := make([]byte, 512*1024)
	base := 0x7FC0
	copy(b[base:], pad([]byte("SUPER MARIO WORLD"), 21))
	var checksum uint16 = 0x1234
	complement := ^checksum
	if !valid {
		complement = 0x0000
	}
	binary.LittleEndian.PutUint16(b[base+0x1C:], complement)
	binary.LittleEndian.PutUint16(b[base+0x1E:], checksum)
	return b
}

func mkvFile() []byte {
	return append([]byte{0x1A, 0x45, 0xDF, 0xA3}, make([]byte, 4096)...)
}

func mp4File() []byte {
	return append([]byte("\x00\x00\x00\x20ftypisom"), make([]byte, 4096)...)
}

func srtFile() []byte {
	return []byte("1\n00:00:01,000 --> 00:00:04,000\nHello.\n")
}

// --- ISO9660 ---------------------------------------------------------------

const isoBlock = 2048

type isoFile struct {
	name    string
	content string
	isDir   bool
}

// buildISO lays out the smallest ISO9660 volume the disc probe will accept: a
// primary volume descriptor at block 16 whose root record points at a directory
// extent, and one data block per file.
func buildISO(files []isoFile) []byte {
	const (
		pvdLBA  = 16
		rootLBA = 17
		dataLBA = 18
	)
	img := make([]byte, (dataLBA+len(files)+2)*isoBlock)

	pvd := img[pvdLBA*isoBlock:]
	pvd[0] = 1
	copy(pvd[1:], "CD001")
	root := pvd[156:190]
	root[0] = 34
	binary.LittleEndian.PutUint32(root[2:6], rootLBA)
	binary.LittleEndian.PutUint32(root[10:14], isoBlock)
	root[25] = 0x02
	root[32] = 1

	dir := img[rootLBA*isoBlock:]
	off := 0
	for i, f := range files {
		name := f.name
		if !f.isDir {
			name += ";1"
		}
		recLen := 33 + len(name)
		if recLen%2 == 1 {
			recLen++
		}
		rec := dir[off : off+recLen]
		rec[0] = byte(recLen)
		binary.LittleEndian.PutUint32(rec[2:6], uint32(dataLBA+i))
		binary.LittleEndian.PutUint32(rec[10:14], uint32(len(f.content)))
		if f.isDir {
			rec[25] = 0x02
		}
		rec[32] = byte(len(name))
		copy(rec[33:], name)
		off += recLen

		copy(img[(dataLBA+i)*isoBlock:], f.content)
	}
	return img
}

func ps1ISO() []byte {
	return buildISO([]isoFile{
		{name: "SYSTEM.CNF", content: "BOOT = cdrom:\\SLUS_007.57;1\r\nTCB = 4\r\n"},
	})
}

func pspISO() []byte {
	return buildISO([]isoFile{
		{name: "UMD_DATA.BIN", content: strings.Repeat("x", 64)},
		{name: "PSP_GAME", isDir: true},
	})
}

func psxCNFasPSP() []byte {
	return buildISO([]isoFile{
		{name: "SYSTEM.CNF", content: "BOOT2 = disc0:/PSP_GAME/SYSDIR/EBOOT.BIN\r\n"},
	})
}

func dreamcastImage() []byte {
	b := make([]byte, 64*1024)
	copy(b, "SEGA SEGAKATANA SEGA ENTERPRISES")
	return b
}
