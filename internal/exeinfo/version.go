package exeinfo

import (
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"unicode/utf16"
)

type vblock struct {
	key      string
	typ      uint16
	value    []byte
	children []vblock
}

func align4(n int) int { return (n + 3) &^ 3 }

// parseVBlock parses one VS_VERSIONINFO style block. It returns the block
// and its total length.
func parseVBlock(b []byte, depth int) (vblock, int, error) {
	if len(b) < 6 || depth > 6 {
		return vblock{}, 0, errors.New("short version block")
	}
	length := int(binary.LittleEndian.Uint16(b[0:]))
	valLen := int(binary.LittleEndian.Uint16(b[2:]))
	typ := binary.LittleEndian.Uint16(b[4:])
	if length < 6 || length > len(b) {
		length = len(b)
	}
	b = b[:length]
	// key: NUL terminated UTF-16
	var key []uint16
	off := 6
	for off+1 < len(b) {
		c := binary.LittleEndian.Uint16(b[off:])
		off += 2
		if c == 0 {
			break
		}
		key = append(key, c)
	}
	blk := vblock{key: string(utf16.Decode(key)), typ: typ}
	off = align4(off)
	vbytes := valLen
	if typ == 1 {
		vbytes = valLen * 2 // text values count WORDs
	}
	if off+vbytes > len(b) {
		vbytes = len(b) - off
		if vbytes < 0 {
			vbytes = 0
		}
	}
	if off <= len(b) {
		blk.value = b[off : off+vbytes]
	}
	off = align4(off + vbytes)
	for off+6 <= len(b) {
		child, n, err := parseVBlock(b[off:], depth+1)
		if err != nil || n == 0 {
			break
		}
		blk.children = append(blk.children, child)
		off = align4(off + n)
	}
	return blk, length, nil
}

func utf16String(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return strings.TrimSpace(string(utf16.Decode(u)))
}

// ParseVersionInfo returns the strings (ProductName, FileDescription, ...)
// of a raw RT_VERSION resource. English tables win when there are several.
func ParseVersionInfo(b []byte) (map[string]string, error) {
	root, _, err := parseVBlock(b, 0)
	if err != nil {
		return nil, err
	}
	if root.key != "VS_VERSION_INFO" {
		return nil, errors.New("not a version resource")
	}
	var tables []vblock
	for _, c := range root.children {
		if c.key == "StringFileInfo" {
			tables = append(tables, c.children...)
		}
	}
	if len(tables) == 0 {
		return nil, ErrNoResource
	}
	pick := tables[0]
	for _, t := range tables {
		if strings.HasPrefix(strings.ToLower(t.key), "0409") {
			pick = t
			break
		}
	}
	out := map[string]string{}
	for _, s := range pick.children {
		if v := utf16String(s.value); v != "" {
			out[s.key] = v
		}
	}
	return out, nil
}

// VersionStrings reads the version info strings of an exe.
func VersionStrings(path string) (map[string]string, error) {
	r, err := openResources(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := r.first(rtVersion)
	if err != nil {
		return nil, err
	}
	return ParseVersionInfo(data)
}

var (
	nameMu    sync.Mutex
	nameCache = map[string][2]string{}
)

// ProductAndDescription returns ProductName and FileDescription of an exe
// (both may be empty). Results are cached per path.
func ProductAndDescription(path string) (string, string) {
	nameMu.Lock()
	if v, ok := nameCache[path]; ok {
		nameMu.Unlock()
		return v[0], v[1]
	}
	nameMu.Unlock()
	m, _ := VersionStrings(path)
	v := [2]string{m["ProductName"], m["FileDescription"]}
	nameMu.Lock()
	if len(nameCache) > 2048 {
		nameCache = map[string][2]string{}
	}
	nameCache[path] = v
	nameMu.Unlock()
	return v[0], v[1]
}
