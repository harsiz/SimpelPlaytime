package exeinfo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"

	"github.com/harsiz/SimpelPlaytime/internal/fsutil"
)

type iconChoice struct {
	width    int
	bitCount int
	id       uint32 // RT_ICON id (for exe groups)
	offset   uint32 // data offset (for .ico files)
	size     uint32
}

func better(a, b iconChoice) bool {
	// prefer the biggest icon up to 256 px, then the most colours
	if a.width != b.width {
		return a.width > b.width
	}
	return a.bitCount > b.bitCount
}

// Icon returns the main icon of an exe (the biggest, best one of the first
// icon group, which is what Explorer shows).
func Icon(path string) (image.Image, error) {
	r, err := openResources(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	grp, err := r.first(rtGroupIcon)
	if err != nil {
		return nil, err
	}
	choices, err := parseGroup(grp)
	if err != nil {
		return nil, err
	}
	var lastErr error = ErrNoResource
	for _, c := range choices {
		data, err := r.find(rtIcon, c.id)
		if err != nil {
			lastErr = err
			continue
		}
		img, err := DecodeIconImage(data)
		if err != nil {
			lastErr = err
			continue
		}
		return img, nil
	}
	return nil, lastErr
}

// parseGroup reads a GRPICONDIR and returns the entries best first.
func parseGroup(b []byte) ([]iconChoice, error) {
	if len(b) < 6 {
		return nil, errors.New("short icon group")
	}
	n := int(binary.LittleEndian.Uint16(b[4:]))
	if len(b) < 6+n*14 {
		return nil, errors.New("truncated icon group")
	}
	var out []iconChoice
	for i := 0; i < n; i++ {
		e := b[6+i*14:]
		w := int(e[0])
		if w == 0 {
			w = 256
		}
		out = append(out, iconChoice{
			width:    w,
			bitCount: int(binary.LittleEndian.Uint16(e[6:])),
			size:     binary.LittleEndian.Uint32(e[8:]),
			id:       uint32(binary.LittleEndian.Uint16(e[12:])),
		})
	}
	sortChoices(out)
	return out, nil
}

func sortChoices(c []iconChoice) {
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && better(c[j], c[j-1]); j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}

// DecodeICO decodes the best image of an .ico file.
func DecodeICO(b []byte) (image.Image, error) {
	if len(b) < 6 || binary.LittleEndian.Uint16(b[0:]) != 0 || binary.LittleEndian.Uint16(b[2:]) != 1 {
		return nil, errors.New("not an ico file")
	}
	n := int(binary.LittleEndian.Uint16(b[4:]))
	if len(b) < 6+n*16 {
		return nil, errors.New("truncated ico")
	}
	var choices []iconChoice
	for i := 0; i < n; i++ {
		e := b[6+i*16:]
		w := int(e[0])
		if w == 0 {
			w = 256
		}
		choices = append(choices, iconChoice{
			width:    w,
			bitCount: int(binary.LittleEndian.Uint16(e[6:])),
			size:     binary.LittleEndian.Uint32(e[8:]),
			offset:   binary.LittleEndian.Uint32(e[12:]),
		})
	}
	sortChoices(choices)
	var lastErr error = errors.New("no images in ico")
	for _, c := range choices {
		end := uint64(c.offset) + uint64(c.size)
		if end > uint64(len(b)) || c.size == 0 {
			lastErr = errors.New("ico entry out of range")
			continue
		}
		img, err := DecodeIconImage(b[c.offset:end])
		if err == nil {
			return img, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// DecodeIconImage decodes one icon image, either PNG or a DIB with AND mask.
func DecodeIconImage(b []byte) (image.Image, error) {
	if bytes.HasPrefix(b, pngMagic) {
		return png.Decode(bytes.NewReader(b))
	}
	return decodeDIB(b)
}

func decodeDIB(b []byte) (image.Image, error) {
	if len(b) < 40 {
		return nil, errors.New("short dib")
	}
	hdrSize := int(binary.LittleEndian.Uint32(b[0:]))
	if hdrSize < 40 || hdrSize > len(b) {
		return nil, fmt.Errorf("bad dib header size %d", hdrSize)
	}
	w := int(int32(binary.LittleEndian.Uint32(b[4:])))
	h2 := int(int32(binary.LittleEndian.Uint32(b[8:])))
	bpp := int(binary.LittleEndian.Uint16(b[14:]))
	compression := binary.LittleEndian.Uint32(b[16:])
	clrUsed := int(binary.LittleEndian.Uint32(b[32:]))
	topDown := h2 < 0
	if topDown {
		h2 = -h2
	}
	h := h2 / 2 // the height covers the XOR image plus the AND mask
	if w <= 0 || h <= 0 || w > 1024 || h > 1024 {
		return nil, fmt.Errorf("bad dib size %dx%d", w, h)
	}
	if compression != 0 && compression != 3 {
		return nil, fmt.Errorf("unsupported dib compression %d", compression)
	}
	off := hdrSize
	if compression == 3 && hdrSize == 40 {
		off += 12 // bit field masks
	}
	var palette []color.NRGBA
	if bpp <= 8 {
		n := clrUsed
		if n == 0 || n > 1<<bpp {
			n = 1 << bpp
		}
		if off+n*4 > len(b) {
			return nil, errors.New("truncated palette")
		}
		for i := 0; i < n; i++ {
			p := b[off+i*4:]
			palette = append(palette, color.NRGBA{R: p[2], G: p[1], B: p[0], A: 255})
		}
		off += n * 4
	}
	switch bpp {
	case 1, 4, 8, 16, 24, 32:
	default:
		return nil, fmt.Errorf("unsupported bit depth %d", bpp)
	}
	xorStride := ((w*bpp + 31) / 32) * 4
	andStride := ((w + 31) / 32) * 4
	xorSize := xorStride * h
	if off+xorSize > len(b) {
		return nil, errors.New("truncated dib pixels")
	}
	xor := b[off : off+xorSize]
	var and []byte
	if off+xorSize+andStride*h <= len(b) {
		and = b[off+xorSize : off+xorSize+andStride*h]
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	anyAlpha := false
	for y := 0; y < h; y++ {
		srcY := h - 1 - y
		if topDown {
			srcY = y
		}
		row := xor[srcY*xorStride:]
		for x := 0; x < w; x++ {
			var c color.NRGBA
			switch bpp {
			case 32:
				p := row[x*4:]
				c = color.NRGBA{R: p[2], G: p[1], B: p[0], A: p[3]}
				if p[3] != 0 {
					anyAlpha = true
				}
			case 24:
				p := row[x*3:]
				c = color.NRGBA{R: p[2], G: p[1], B: p[0], A: 255}
			case 16:
				v := binary.LittleEndian.Uint16(row[x*2:])
				c = color.NRGBA{R: uint8((v >> 10 & 0x1f) << 3), G: uint8((v >> 5 & 0x1f) << 3), B: uint8((v & 0x1f) << 3), A: 255}
			case 8:
				c = pal(palette, int(row[x]))
			case 4:
				v := row[x/2]
				if x%2 == 0 {
					v >>= 4
				}
				c = pal(palette, int(v&0x0f))
			case 1:
				v := row[x/8] >> (7 - uint(x%8)) & 1
				c = pal(palette, int(v))
			}
			img.SetNRGBA(x, y, c)
		}
	}
	// apply the AND mask unless a 32 bit image already has real alpha
	if and != nil && !(bpp == 32 && anyAlpha) {
		for y := 0; y < h; y++ {
			srcY := h - 1 - y
			if topDown {
				srcY = y
			}
			row := and[srcY*andStride:]
			for x := 0; x < w; x++ {
				transparent := row[x/8]>>(7-uint(x%8))&1 == 1
				c := img.NRGBAAt(x, y)
				if transparent {
					c.A = 0
				} else {
					c.A = 255
				}
				img.SetNRGBA(x, y, c)
			}
		}
	}
	return img, nil
}

func pal(p []color.NRGBA, i int) color.NRGBA {
	if i < len(p) {
		return p[i]
	}
	return color.NRGBA{A: 255}
}

// Resize scales an image down so its longest side is at most max pixels.
func Resize(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return img
	}
	nw, nh := max, max
	if w > h {
		nh = h * max / w
	} else {
		nw = w * max / h
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

// SaveIconPNG extracts the icon of an exe (or reads an .ico/.png file) and
// writes it as a PNG of at most maxSize pixels.
func SaveIconPNG(src, out string, maxSize int) error {
	img, err := LoadIcon(src)
	if err != nil {
		return err
	}
	img = Resize(img, maxSize)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(out, buf.Bytes(), 0o644)
}

// LoadIcon reads an icon from an exe, .ico or .png file.
func LoadIcon(src string) (image.Image, error) {
	switch filepath.Ext(src) {
	case ".ico", ".ICO":
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, err
		}
		return DecodeICO(data)
	case ".png", ".PNG":
		f, err := os.Open(src)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return png.Decode(f)
	}
	return Icon(src)
}
