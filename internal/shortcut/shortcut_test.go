package shortcut

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func le16(b *bytes.Buffer, v uint16) { binary.Write(b, binary.LittleEndian, v) }
func le32(b *bytes.Buffer, v uint32) { binary.Write(b, binary.LittleEndian, v) }

func ustr(b *bytes.Buffer, s string) {
	u := utf16.Encode([]rune(s))
	le16(b, uint16(len(u)))
	for _, c := range u {
		le16(b, c)
	}
}

type lnkOpts struct {
	base, suffix string // link info (ANSI)
	envTarget    string // env block
	args, icon   string
	idList       bool
}

func buildLNK(o lnkOpts) []byte {
	var b bytes.Buffer
	flags := uint32(isUnicode)
	if o.base != "" {
		flags |= hasLinkInfo
	}
	if o.args != "" {
		flags |= hasArguments
	}
	if o.icon != "" {
		flags |= hasIconLocation
	}
	if o.idList {
		flags |= hasLinkTargetIDList
	}
	le32(&b, 0x4C)
	b.Write(lnkCLSID)
	le32(&b, flags)
	b.Write(make([]byte, 76-b.Len()))
	if o.idList {
		le16(&b, 4)
		b.Write([]byte{1, 2, 3, 4})
	}
	if o.base != "" {
		// header (28 bytes) + volume id (dummy 16 bytes) + base + suffix
		volOff := 28
		baseOff := volOff + 16
		suffixOff := baseOff + len(o.base) + 1
		size := suffixOff + len(o.suffix) + 1
		le32(&b, uint32(size))
		le32(&b, 28)
		le32(&b, 1)
		le32(&b, uint32(volOff))
		le32(&b, uint32(baseOff))
		le32(&b, 0)
		le32(&b, uint32(suffixOff))
		b.Write(make([]byte, 16))
		b.WriteString(o.base)
		b.WriteByte(0)
		b.WriteString(o.suffix)
		b.WriteByte(0)
	}
	if o.args != "" {
		ustr(&b, o.args)
	}
	if o.icon != "" {
		ustr(&b, o.icon)
	}
	if o.envTarget != "" {
		le32(&b, 8+260+520)
		le32(&b, 0xA0000001)
		ansi := make([]byte, 260)
		copy(ansi, o.envTarget)
		b.Write(ansi)
		uni := make([]byte, 520)
		for i, c := range utf16.Encode([]rune(o.envTarget)) {
			binary.LittleEndian.PutUint16(uni[i*2:], c)
		}
		b.Write(uni)
	}
	le32(&b, 0) // terminal block
	return b.Bytes()
}

func TestParseLNKLinkInfo(t *testing.T) {
	l, err := ParseLNK(buildLNK(lnkOpts{base: `C:\Games\Hades II\`, suffix: "Hades2.exe", args: "-windowed", icon: `C:\Games\Hades II\Hades2.exe,0`, idList: true}))
	if err != nil {
		t.Fatal(err)
	}
	if l.Target != `C:\Games\Hades II\Hades2.exe` || l.Arguments != "-windowed" || !strings.HasSuffix(l.IconLocation, ",0") {
		t.Errorf("link = %+v", l)
	}
}

func TestParseLNKEnvBlock(t *testing.T) {
	l, err := ParseLNK(buildLNK(lnkOpts{envTarget: `%ProgramFiles%\Cool\cool.exe`}))
	if err != nil {
		t.Fatal(err)
	}
	got := ExpandEnv(l.Target, func(k string) string {
		if k == "ProgramFiles" {
			return `C:\Program Files`
		}
		return ""
	})
	if got != `C:\Program Files\Cool\cool.exe` {
		t.Errorf("target = %q", got)
	}
	if ExpandEnv("%NOPE%\\x", func(string) string { return "" }) != `%NOPE%\x` {
		t.Error("unknown vars should stay")
	}
}

func TestParseLNKErrors(t *testing.T) {
	if _, err := ParseLNK([]byte("hello")); err == nil {
		t.Error("garbage should fail")
	}
	b := buildLNK(lnkOpts{base: `C:\x\`, suffix: "y.exe", args: "zzz"})
	if _, err := ParseLNK(b[:len(b)-12]); err == nil {
		t.Error("truncated link should fail")
	}
}

func TestResolveFiles(t *testing.T) {
	dir := t.TempDir()
	lnk := filepath.Join(dir, "Hades II.lnk")
	os.WriteFile(lnk, buildLNK(lnkOpts{base: `C:\Games\Hades II\`, suffix: "Hades2.exe"}), 0o644)
	tg, err := Resolve(lnk)
	if err != nil {
		t.Fatal(err)
	}
	if tg.Kind != TargetExe || tg.Name != "Hades II" || tg.Path != `C:\Games\Hades II\Hades2.exe` {
		t.Errorf("lnk target = %+v", tg)
	}

	notExe := filepath.Join(dir, "Doc.lnk")
	os.WriteFile(notExe, buildLNK(lnkOpts{base: `C:\Docs\`, suffix: "a.pdf"}), 0o644)
	if _, err := Resolve(notExe); err == nil {
		t.Error("shortcut to a pdf should complain")
	}

	steam := filepath.Join(dir, "Hades.url")
	os.WriteFile(steam, []byte("[{000214A0-0000-0000-C000-000000000046}]\nProp3=19,0\n[InternetShortcut]\nIDList=\nIconIndex=0\nURL=steam://rungameid/1145360\nIconFile=C:\\Steam\\steam\\games\\abc.ico\n"), 0o644)
	tg, err = Resolve(steam)
	if err != nil {
		t.Fatal(err)
	}
	if tg.Kind != TargetSteam || tg.SteamAppID != 1145360 || tg.Name != "Hades" || !strings.HasSuffix(tg.IconPath, "abc.ico") {
		t.Errorf("steam url = %+v", tg)
	}

	exe := filepath.Join(dir, "thing.exe")
	if tg, _ := Resolve(exe); tg.Kind != TargetExe || tg.Path != exe || tg.Name != "thing" {
		t.Errorf("exe = %+v", tg)
	}
	if _, err := Resolve(filepath.Join(dir, "x.txt")); err == nil {
		t.Error("txt should fail")
	}
}

func TestClassifyURL(t *testing.T) {
	epic := ClassifyURL("com.epicgames.launcher://apps/fn%3A4fe75bbc5a674f4f9b356b5c90567da5%3AFortnite?action=launch&silent=true")
	if epic.Kind != TargetEpic || epic.EpicApp != "Fortnite" {
		t.Errorf("epic = %+v", epic)
	}
	simple := ClassifyURL("com.epicgames.launcher://apps/Sugar?action=launch")
	if simple.EpicApp != "Sugar" {
		t.Errorf("simple epic = %+v", simple)
	}
	file := ClassifyURL("file:///C:/Games/x/x.exe")
	if file.Kind != TargetExe || filepath.ToSlash(file.Path) != "C:/Games/x/x.exe" {
		t.Errorf("file = %+v", file)
	}
	if o := ClassifyURL("https://example.com"); o.Kind != TargetOther {
		t.Errorf("web = %+v", o)
	}
	if s := ClassifyURL("steam://run/570"); s.SteamAppID != 570 {
		t.Errorf("steam run = %+v", s)
	}
	if _, _, err := ParseURL([]byte("[Other]\nURL=x")); err == nil {
		t.Error("url outside section should not count")
	}
}
