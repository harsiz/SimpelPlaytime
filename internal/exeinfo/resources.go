// Package exeinfo reads icons and version info straight out of Windows .exe
// files (PE resources). It is pure Go, so it also works (and is tested) on
// other platforms, and it reads only the bytes it needs, so even huge game
// executables are cheap to inspect.
package exeinfo

import (
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf16"
)

// Resource type ids.
const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
)

// ErrNoResource is returned when the exe has no such resource.
var ErrNoResource = errors.New("resource not found")

const maxResourceSize = 32 << 20

type resReader struct {
	f        *os.File
	pf       *pe.File
	sections []*pe.Section
	root     int64 // file offset of the resource directory root
	rootRVA  uint32
	rootSec  *pe.Section
}

func openResources(path string) (*resReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r, err := newResReader(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return r, nil
}

func newResReader(f *os.File) (*resReader, error) {
	pf, err := pe.NewFile(f)
	if err != nil {
		return nil, err
	}
	var dd pe.DataDirectory
	switch oh := pf.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		if oh.NumberOfRvaAndSizes <= 2 {
			return nil, ErrNoResource
		}
		dd = oh.DataDirectory[2]
	case *pe.OptionalHeader64:
		if oh.NumberOfRvaAndSizes <= 2 {
			return nil, ErrNoResource
		}
		dd = oh.DataDirectory[2]
	default:
		return nil, ErrNoResource
	}
	if dd.VirtualAddress == 0 || dd.Size == 0 {
		return nil, ErrNoResource
	}
	r := &resReader{f: f, pf: pf, sections: pf.Sections, rootRVA: dd.VirtualAddress}
	sec, off, ok := r.rvaToSection(dd.VirtualAddress)
	if !ok {
		return nil, ErrNoResource
	}
	r.rootSec = sec
	r.root = off
	return r, nil
}

func (r *resReader) Close() error { return r.f.Close() }

// rvaToSection returns the section holding rva and the offset inside it.
func (r *resReader) rvaToSection(rva uint32) (*pe.Section, int64, bool) {
	for _, s := range r.sections {
		size := s.VirtualSize
		if s.Size > size {
			size = s.Size
		}
		if rva >= s.VirtualAddress && rva < s.VirtualAddress+size {
			return s, int64(rva - s.VirtualAddress), true
		}
	}
	return nil, 0, false
}

// readDir reads bytes at an offset relative to the resource root.
func (r *resReader) readDir(off uint32, n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := r.rootSec.ReadAt(b, r.root+int64(off)); err != nil && err != io.EOF {
		return nil, err
	}
	return b, nil
}

type dirEntry struct {
	id     uint32
	name   string
	offset uint32 // relative to root
	isDir  bool
}

func (r *resReader) entries(off uint32) ([]dirEntry, error) {
	hdr, err := r.readDir(off, 16)
	if err != nil {
		return nil, err
	}
	named := int(binary.LittleEndian.Uint16(hdr[12:]))
	ids := int(binary.LittleEndian.Uint16(hdr[14:]))
	n := named + ids
	if n > 4096 {
		return nil, fmt.Errorf("too many resource entries (%d)", n)
	}
	raw, err := r.readDir(off+16, n*8)
	if err != nil {
		return nil, err
	}
	out := make([]dirEntry, 0, n)
	for i := 0; i < n; i++ {
		nameField := binary.LittleEndian.Uint32(raw[i*8:])
		dataField := binary.LittleEndian.Uint32(raw[i*8+4:])
		e := dirEntry{offset: dataField &^ 0x80000000, isDir: dataField&0x80000000 != 0}
		if nameField&0x80000000 != 0 {
			e.name = r.readName(nameField &^ 0x80000000)
		} else {
			e.id = nameField
		}
		out = append(out, e)
	}
	return out, nil
}

func (r *resReader) readName(off uint32) string {
	lb, err := r.readDir(off, 2)
	if err != nil {
		return ""
	}
	n := int(binary.LittleEndian.Uint16(lb))
	b, err := r.readDir(off+2, n*2)
	if err != nil {
		return ""
	}
	u := make([]uint16, n)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u))
}

// leafData follows a directory entry down to the first data blob.
func (r *resReader) leafData(e dirEntry) ([]byte, error) {
	for depth := 0; e.isDir; depth++ {
		if depth > 4 {
			return nil, errors.New("resource tree too deep")
		}
		sub, err := r.entries(e.offset)
		if err != nil {
			return nil, err
		}
		if len(sub) == 0 {
			return nil, ErrNoResource
		}
		e = sub[0]
	}
	de, err := r.readDir(e.offset, 16)
	if err != nil {
		return nil, err
	}
	rva := binary.LittleEndian.Uint32(de[0:])
	size := binary.LittleEndian.Uint32(de[4:])
	if size == 0 || size > maxResourceSize {
		return nil, fmt.Errorf("bad resource size %d", size)
	}
	sec, off, ok := r.rvaToSection(rva)
	if !ok {
		return nil, errors.New("resource data outside sections")
	}
	b := make([]byte, size)
	if _, err := sec.ReadAt(b, off); err != nil && err != io.EOF {
		return nil, err
	}
	return b, nil
}

// typeEntries returns the named/numbered resources of one type.
func (r *resReader) typeEntries(typ uint32) ([]dirEntry, error) {
	top, err := r.entries(0)
	if err != nil {
		return nil, err
	}
	for _, t := range top {
		if t.name == "" && t.id == typ && t.isDir {
			return r.entries(t.offset)
		}
	}
	return nil, ErrNoResource
}

// find returns the data of the resource with the given type and id.
func (r *resReader) find(typ, id uint32) ([]byte, error) {
	items, err := r.typeEntries(typ)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.name == "" && it.id == id {
			return r.leafData(it)
		}
	}
	return nil, ErrNoResource
}

// first returns the data of the first resource of a type.
func (r *resReader) first(typ uint32) ([]byte, error) {
	items, err := r.typeEntries(typ)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNoResource
	}
	return r.leafData(items[0])
}
