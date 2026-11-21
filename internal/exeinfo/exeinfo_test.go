package exeinfo

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"unicode/utf16"
)

type testRes struct {
	typ, id uint32
	data    []byte
}

func le16(b *bytes.Buffer, v uint16) { binary.Write(b, binary.LittleEndian, v) }
func le32(b *bytes.Buffer, v uint32) { binary.Write(b, binary.LittleEndian, v) }

// buildRsrc lays out a resource section (type -> id -> lang -> data).
func buildRsrc(res []testRes, va uint32) []byte {
	byType := map[uint32][]testRes{}
	var types []uint32
	for _, r := range res {
		if _, ok := byType[r.typ]; !ok {
			types = append(types, r.typ)
		}
		byType[r.typ] = append(byType[r.typ], r)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	for _, t := range types {
		rs := byType[t]
		sort.Slice(rs, func(i, j int) bool { return rs[i].id < rs[j].id })
	}
	// offsets
	off := uint32(16 + 8*len(types))
	typeDirOff := map[uint32]uint32{}
	for _, t := range types {
		typeDirOff[t] = off
		off += uint32(16 + 8*len(byType[t]))
	}
	langDirOff := map[[2]uint32]uint32{}
	for _, t := range types {
		for _, r := range byType[t] {
			langDirOff[[2]uint32{t, r.id}] = off
			off += 16 + 8
		}
	}
	dataEntryOff := map[[2]uint32]uint32{}
	for _, t := range types {
		for _, r := range byType[t] {
			dataEntryOff[[2]uint32{t, r.id}] = off
			off += 16
		}
	}
	blobOff := map[[2]uint32]uint32{}
	for _, t := range types {
		for _, r := range byType[t] {
			off = (off + 7) &^ 7
			blobOff[[2]uint32{t, r.id}] = off
			off += uint32(len(r.data))
		}
	}
	var b bytes.Buffer
	dirHdr := func(n int) {
		le32(&b, 0)
		le32(&b, 0)
		le16(&b, 0)
		le16(&b, 0)
		le16(&b, 0)
		le16(&b, uint16(n))
	}
	dirHdr(len(types))
	for _, t := range types {
		le32(&b, t)
		le32(&b, typeDirOff[t]|0x80000000)
	}
	for _, t := range types {
		dirHdr(len(byType[t]))
		for _, r := range byType[t] {
			le32(&b, r.id)
			le32(&b, langDirOff[[2]uint32{t, r.id}]|0x80000000)
		}
	}
	for _, t := range types {
		for _, r := range byType[t] {
			dirHdr(1)
			le32(&b, 1033)
			le32(&b, dataEntryOff[[2]uint32{t, r.id}])
		}
	}
	for _, t := range types {
		for _, r := range byType[t] {
			le32(&b, va+blobOff[[2]uint32{t, r.id}])
			le32(&b, uint32(len(r.data)))
			le32(&b, 0)
			le32(&b, 0)
		}
	}
	for _, t := range types {
		for _, r := range byType[t] {
			for uint32(b.Len()) < blobOff[[2]uint32{t, r.id}] {
				b.WriteByte(0)
			}
			b.Write(r.data)
		}
	}
	return b.Bytes()
}

// buildPE writes a minimal PE32+ file with one .rsrc section.
func buildPE(t *testing.T, res []testRes) string {
	t.Helper()
	const va = 0x1000
	rsrc := buildRsrc(res, va)
	raw := (len(rsrc) + 0x1ff) &^ 0x1ff
	var b bytes.Buffer
	b.Write([]byte{'M', 'Z'})
	b.Write(make([]byte, 0x3c-2))
	le32(&b, 0x80)
	b.Write(make([]byte, 0x80-b.Len()))
	b.WriteString("PE\x00\x00")
	oh := pe.OptionalHeader64{
		Magic: 0x20b, SectionAlignment: 0x1000, FileAlignment: 0x200,
		SizeOfImage: uint32(va + ((len(rsrc) + 0xfff) &^ 0xfff)), SizeOfHeaders: 0x200,
		NumberOfRvaAndSizes: 16, Subsystem: 2,
	}
	oh.DataDirectory[2] = pe.DataDirectory{VirtualAddress: va, Size: uint32(len(rsrc))}
	fh := pe.FileHeader{Machine: 0x8664, NumberOfSections: 1, SizeOfOptionalHeader: uint16(binary.Size(oh)), Characteristics: 0x22}
	binary.Write(&b, binary.LittleEndian, fh)
	binary.Write(&b, binary.LittleEndian, oh)
	sh := pe.SectionHeader32{VirtualSize: uint32(len(rsrc)), VirtualAddress: va, SizeOfRawData: uint32(raw), PointerToRawData: 0x200, Characteristics: 0x40000040}
	copy(sh.Name[:], ".rsrc")
	binary.Write(&b, binary.LittleEndian, sh)
	b.Write(make([]byte, 0x200-b.Len()))
	b.Write(rsrc)
	b.Write(make([]byte, raw-len(rsrc)))
	p := filepath.Join(t.TempDir(), "test.exe")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// dib32 makes a 32 bit DIB icon image filled with c.
func dib32(w, h int, c color.NRGBA) []byte {
	var b bytes.Buffer
	le32(&b, 40)
	le32(&b, uint32(w))
	le32(&b, uint32(h*2))
	le16(&b, 1)
	le16(&b, 32)
	for i := 0; i < 6; i++ {
		le32(&b, 0)
	}
	for i := 0; i < w*h; i++ {
		b.Write([]byte{c.B, c.G, c.R, c.A})
	}
	b.Write(make([]byte, ((w+31)/32)*4*h))
	return b.Bytes()
}

// dib24 makes a 24 bit DIB whose left half is masked transparent.
func dib24(w, h int) []byte {
	var b bytes.Buffer
	le32(&b, 40)
	le32(&b, uint32(w))
	le32(&b, uint32(h*2))
	le16(&b, 1)
	le16(&b, 24)
	for i := 0; i < 6; i++ {
		le32(&b, 0)
	}
	stride := ((w*24 + 31) / 32) * 4
	for y := 0; y < h; y++ {
		row := make([]byte, stride)
		for x := 0; x < w; x++ {
			row[x*3+2] = 200 // red
		}
		b.Write(row)
	}
	andStride := ((w + 31) / 32) * 4
	for y := 0; y < h; y++ {
		row := make([]byte, andStride)
		for x := 0; x < w/2; x++ {
			row[x/8] |= 0x80 >> uint(x%8)
		}
		b.Write(row)
	}
	return b.Bytes()
}

func pngBytes(w, h int, c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func group(entries ...[3]int) []byte { // width, bitcount, id
	var b bytes.Buffer
	le16(&b, 0)
	le16(&b, 1)
	le16(&b, uint16(len(entries)))
	for _, e := range entries {
		w := byte(e[0])
		if e[0] >= 256 {
			w = 0
		}
		b.Write([]byte{w, w, 0, 0})
		le16(&b, 1)
		le16(&b, uint16(e[1]))
		le32(&b, 1000)
		le16(&b, uint16(e[2]))
	}
	return b.Bytes()
}

func u16z(s string) []byte {
	var b bytes.Buffer
	for _, c := range utf16.Encode([]rune(s)) {
		le16(&b, c)
	}
	le16(&b, 0)
	return b.Bytes()
}

func pad4(b *bytes.Buffer) {
	for b.Len()%4 != 0 {
		b.WriteByte(0)
	}
}

func vblockBytes(key string, typ uint16, value []byte, valLen int, children ...[]byte) []byte {
	var b bytes.Buffer
	le16(&b, 0) // patched below
	le16(&b, uint16(valLen))
	le16(&b, typ)
	b.Write(u16z(key))
	pad4(&b)
	b.Write(value)
	for _, c := range children {
		pad4(&b)
		b.Write(c)
	}
	out := b.Bytes()
	binary.LittleEndian.PutUint16(out, uint16(len(out)))
	return out
}

func strBlock(k, v string) []byte {
	val := u16z(v)
	return vblockBytes(k, 1, val, len(val)/2)
}

func versionRes() []byte {
	fixed := make([]byte, 52)
	binary.LittleEndian.PutUint32(fixed, 0xFEEF04BD)
	table := vblockBytes("040904b0", 1, nil, 0,
		strBlock("CompanyName", "Supergiant Games"),
		strBlock("FileDescription", "Hades II"),
		strBlock("ProductName", "Hades II (Early Access)"),
	)
	other := vblockBytes("040704b0", 1, nil, 0, strBlock("ProductName", "Deutsch"))
	sfi := vblockBytes("StringFileInfo", 1, nil, 0, other, table)
	return vblockBytes("VS_VERSION_INFO", 0, fixed, len(fixed), sfi)
}

func TestIconFromPE(t *testing.T) {
	red := color.NRGBA{R: 255, A: 255}
	blue := color.NRGBA{B: 255, A: 200}
	p := buildPE(t, []testRes{
		{rtGroupIcon, 1, group([3]int{16, 32, 1}, [3]int{48, 32, 2})},
		{rtIcon, 1, dib32(16, 16, red)},
		{rtIcon, 2, pngBytes(48, 48, blue)},
		{rtVersion, 1, versionRes()},
	})
	img, err := Icon(p)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 48 {
		t.Errorf("should pick the 48px icon, got %v", img.Bounds())
	}
	r, g, b, _ := img.At(10, 10).RGBA()
	if r != 0 || g != 0 || b == 0 {
		t.Errorf("wrong pixel %v", img.At(10, 10))
	}
	m, err := VersionStrings(p)
	if err != nil {
		t.Fatal(err)
	}
	if m["ProductName"] != "Hades II (Early Access)" || m["FileDescription"] != "Hades II" || m["CompanyName"] != "Supergiant Games" {
		t.Errorf("version strings = %v", m)
	}
	prod, desc := ProductAndDescription(p)
	if prod != "Hades II (Early Access)" || desc != "Hades II" {
		t.Errorf("ProductAndDescription = %q %q", prod, desc)
	}
	out := filepath.Join(t.TempDir(), "icon.png")
	if err := SaveIconPNG(p, out, 32); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(out)
	defer f.Close()
	saved, err := png.Decode(f)
	if err != nil || saved.Bounds().Dx() != 32 {
		t.Errorf("saved icon = %v %v", saved.Bounds(), err)
	}
}

func TestDIBWithMask(t *testing.T) {
	img, err := DecodeIconImage(dib24(32, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := img.At(2, 2).RGBA(); a != 0 {
		t.Error("left half should be transparent")
	}
	if r, _, _, a := img.At(30, 2).RGBA(); a == 0 || r>>8 != 200 {
		t.Errorf("right half should be red and opaque, got %v", img.At(30, 2))
	}
}

func TestDIB32KeepsAlpha(t *testing.T) {
	img, err := DecodeIconImage(dib32(8, 8, color.NRGBA{G: 255, A: 128}))
	if err != nil {
		t.Fatal(err)
	}
	c := img.(*image.NRGBA).NRGBAAt(3, 3)
	if c.A != 128 || c.G != 255 {
		t.Errorf("pixel = %v", c)
	}
}

func TestDecodeICO(t *testing.T) {
	small := dib32(16, 16, color.NRGBA{R: 1, A: 255})
	big := pngBytes(64, 64, color.NRGBA{G: 1, A: 255})
	var b bytes.Buffer
	le16(&b, 0)
	le16(&b, 1)
	le16(&b, 2)
	off := uint32(6 + 2*16)
	for _, e := range []struct {
		w    byte
		data []byte
	}{{16, small}, {64, big}} {
		b.Write([]byte{e.w, e.w, 0, 0})
		le16(&b, 1)
		le16(&b, 32)
		le32(&b, uint32(len(e.data)))
		le32(&b, off)
		off += uint32(len(e.data))
	}
	b.Write(small)
	b.Write(big)
	img, err := DecodeICO(b.Bytes())
	if err != nil || img.Bounds().Dx() != 64 {
		t.Fatalf("DecodeICO = %v %v", img, err)
	}
	if _, err := DecodeICO([]byte("nope")); err == nil {
		t.Error("garbage should fail")
	}
}

func TestBrokenInputs(t *testing.T) {
	if _, err := Icon(filepath.Join(t.TempDir(), "missing.exe")); err == nil {
		t.Error("missing file should fail")
	}
	p := filepath.Join(t.TempDir(), "x.exe")
	os.WriteFile(p, []byte("MZ not really"), 0o644)
	if _, err := Icon(p); err == nil {
		t.Error("garbage exe should fail")
	}
	noIcons := buildPE(t, []testRes{{rtVersion, 1, versionRes()}})
	if _, err := Icon(noIcons); err == nil {
		t.Error("exe without icons should fail")
	}
	for _, b := range [][]byte{nil, make([]byte, 39), dib32(0, 0, color.NRGBA{})} {
		if _, err := DecodeIconImage(b); err == nil {
			t.Errorf("DecodeIconImage(%d bytes) should fail", len(b))
		}
	}
	if _, err := ParseVersionInfo([]byte{1, 2, 3}); err == nil {
		t.Error("short version info should fail")
	}
	if p, d := ProductAndDescription(filepath.Join(t.TempDir(), "nope.exe")); p != "" || d != "" {
		t.Error("missing exe should give empty strings")
	}
}

func TestResize(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 256, 128))
	r := Resize(img, 64)
	if r.Bounds().Dx() != 64 || r.Bounds().Dy() != 32 {
		t.Errorf("resize = %v", r.Bounds())
	}
	if Resize(img, 512) != image.Image(img) {
		t.Error("small images should be returned as is")
	}
}
