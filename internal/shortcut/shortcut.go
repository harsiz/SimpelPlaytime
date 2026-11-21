// Package shortcut resolves Windows shortcuts (.lnk) and internet shortcuts
// (.url, used by Steam and Epic for desktop game shortcuts) into something
// Simpel Playtime can track.
package shortcut

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Kind of target.
const (
	TargetExe   = "exe"   // a normal program
	TargetSteam = "steam" // steam://rungameid/<id>
	TargetEpic  = "epic"  // com.epicgames.launcher://apps/<name>
	TargetOther = "other" // some other URL
)

// Target is what a shortcut points at.
type Target struct {
	Kind       string
	Name       string // the shortcut's own name ("Hades II.lnk" -> "Hades II")
	Path       string // exe path for TargetExe
	Args       string
	WorkingDir string
	IconPath   string
	SteamAppID int
	EpicApp    string
	URL        string
}

// Link is the parsed content of a .lnk file.
type Link struct {
	Target       string
	RelativePath string
	WorkingDir   string
	Arguments    string
	IconLocation string
	Name         string
}

const (
	hasLinkTargetIDList = 1 << 0
	hasLinkInfo         = 1 << 1
	hasName             = 1 << 2
	hasRelativePath     = 1 << 3
	hasWorkingDir       = 1 << 4
	hasArguments        = 1 << 5
	hasIconLocation     = 1 << 6
	isUnicode           = 1 << 7
)

var lnkCLSID = []byte{0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}

// ParseLNK parses the Shell Link binary format (MS-SHLLINK).
func ParseLNK(b []byte) (*Link, error) {
	if len(b) < 76 || binary.LittleEndian.Uint32(b) != 0x4C || !bytes.Equal(b[4:20], lnkCLSID) {
		return nil, errors.New("not a shell link")
	}
	flags := binary.LittleEndian.Uint32(b[20:])
	off := 76
	if flags&hasLinkTargetIDList != 0 {
		if off+2 > len(b) {
			return nil, errors.New("truncated id list")
		}
		off += 2 + int(binary.LittleEndian.Uint16(b[off:]))
	}
	l := &Link{}
	if flags&hasLinkInfo != 0 {
		if off+4 > len(b) {
			return nil, errors.New("truncated link info")
		}
		size := int(binary.LittleEndian.Uint32(b[off:]))
		if size < 28 || off+size > len(b) {
			return nil, errors.New("bad link info size")
		}
		l.Target = parseLinkInfo(b[off : off+size])
		off += size
	}
	unicode := flags&isUnicode != 0
	readStr := func() (string, error) {
		if off+2 > len(b) {
			return "", errors.New("truncated string data")
		}
		n := int(binary.LittleEndian.Uint16(b[off:]))
		off += 2
		if unicode {
			if off+n*2 > len(b) {
				return "", errors.New("truncated string")
			}
			u := make([]uint16, n)
			for i := range u {
				u[i] = binary.LittleEndian.Uint16(b[off+i*2:])
			}
			off += n * 2
			return string(utf16.Decode(u)), nil
		}
		if off+n > len(b) {
			return "", errors.New("truncated string")
		}
		s := string(b[off : off+n])
		off += n
		return s, nil
	}
	var err error
	for _, f := range []struct {
		bit uint32
		dst *string
	}{
		{hasName, &l.Name}, {hasRelativePath, &l.RelativePath}, {hasWorkingDir, &l.WorkingDir},
		{hasArguments, &l.Arguments}, {hasIconLocation, &l.IconLocation},
	} {
		if flags&f.bit != 0 {
			if *f.dst, err = readStr(); err != nil {
				return nil, err
			}
		}
	}
	// extra data: the environment variable block holds paths like
	// %ProgramFiles%\Game\game.exe when there is no link info
	for off+8 <= len(b) {
		size := int(binary.LittleEndian.Uint32(b[off:]))
		if size < 8 || off+size > len(b) {
			break
		}
		sig := binary.LittleEndian.Uint32(b[off+4:])
		if sig == 0xA0000001 && size >= 8+260+520 && l.Target == "" {
			blk := b[off+8 : off+size]
			if s := utf16Z(blk[260 : 260+520]); s != "" {
				l.Target = s
			} else {
				l.Target = cString(blk[:260])
			}
		}
		off += size
	}
	return l, nil
}

func parseLinkInfo(b []byte) string {
	hdrSize := binary.LittleEndian.Uint32(b[4:])
	flags := binary.LittleEndian.Uint32(b[8:])
	if flags&1 == 0 { // no VolumeIDAndLocalBasePath
		return ""
	}
	baseOff := int(binary.LittleEndian.Uint32(b[16:]))
	suffixOff := int(binary.LittleEndian.Uint32(b[24:]))
	if hdrSize >= 0x24 && len(b) >= 0x24 {
		uBase := int(binary.LittleEndian.Uint32(b[28:]))
		uSuffix := int(binary.LittleEndian.Uint32(b[32:]))
		if uBase > 0 && uBase < len(b) {
			base := utf16Z(b[uBase:])
			if uSuffix > 0 && uSuffix < len(b) {
				base += utf16Z(b[uSuffix:])
			}
			if base != "" {
				return base
			}
		}
	}
	if baseOff <= 0 || baseOff >= len(b) {
		return ""
	}
	base := cString(b[baseOff:])
	if suffixOff > 0 && suffixOff < len(b) {
		base += cString(b[suffixOff:])
	}
	return base
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func utf16Z(b []byte) string {
	var u []uint16
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

var envRe = regexp.MustCompile(`%([A-Za-z0-9_()]+)%`)

// ExpandEnv expands Windows style %VAR% references.
func ExpandEnv(s string, lookup func(string) string) string {
	return envRe.ReplaceAllStringFunc(s, func(m string) string {
		v := lookup(m[1 : len(m)-1])
		if v == "" {
			return m
		}
		return v
	})
}

// ParseURL parses the INI style .url file and returns URL and IconFile.
func ParseURL(b []byte) (rawURL, iconFile string, err error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	inSection := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inSection = strings.EqualFold(line, "[InternetShortcut]")
			continue
		}
		if !inSection {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "url":
			rawURL = strings.TrimSpace(v)
		case "iconfile":
			iconFile = strings.TrimSpace(v)
		}
	}
	if rawURL == "" {
		return "", "", errors.New("no URL in shortcut")
	}
	return rawURL, iconFile, nil
}

var steamRe = regexp.MustCompile(`^steam://(?:rungameid|run|launch)/(\d+)`)

// ClassifyURL works out what a URL shortcut launches.
func ClassifyURL(raw string) Target {
	t := Target{Kind: TargetOther, URL: raw}
	if m := steamRe.FindStringSubmatch(strings.ToLower(raw)); m != nil {
		if id, err := strconv.Atoi(m[1]); err == nil {
			t.Kind, t.SteamAppID = TargetSteam, id
		}
		return t
	}
	u, err := url.Parse(raw)
	if err != nil {
		return t
	}
	switch strings.ToLower(u.Scheme) {
	case "com.epicgames.launcher":
		// com.epicgames.launcher://apps/<namespace>%3A<id>%3A<AppName>?action=launch
		p := strings.TrimPrefix(u.Opaque, "//")
		if p == "" {
			p = u.Host + u.Path
		}
		p = strings.TrimPrefix(p, "apps/")
		if dec, err := url.PathUnescape(p); err == nil {
			p = dec
		}
		parts := strings.Split(p, ":")
		t.Kind, t.EpicApp = TargetEpic, parts[len(parts)-1]
	case "file":
		path := u.Path
		if len(path) > 2 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
		t.Kind, t.Path = TargetExe, filepath.FromSlash(path)
	}
	return t
}

// Resolve reads a .lnk, .url or .exe and returns its target.
func Resolve(path string) (Target, error) {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe":
		return Target{Kind: TargetExe, Name: name, Path: path}, nil
	case ".lnk":
		b, err := os.ReadFile(path)
		if err != nil {
			return Target{}, err
		}
		l, err := ParseLNK(b)
		if err != nil {
			return Target{}, err
		}
		target := ExpandEnv(l.Target, os.Getenv)
		if target == "" && l.RelativePath != "" {
			target = filepath.Join(filepath.Dir(path), ExpandEnv(l.RelativePath, os.Getenv))
		}
		if target == "" {
			return Target{}, errors.New("this shortcut does not point at a file (it may be a special shell item)")
		}
		t := Target{Kind: TargetExe, Name: name, Path: target, Args: l.Arguments, WorkingDir: l.WorkingDir}
		if i := strings.LastIndex(l.IconLocation, ","); i > 0 {
			t.IconPath = ExpandEnv(l.IconLocation[:i], os.Getenv)
		} else if l.IconLocation != "" {
			t.IconPath = ExpandEnv(l.IconLocation, os.Getenv)
		}
		// shortcuts to launchers with a steam url argument
		if m := steamRe.FindStringSubmatch(strings.ToLower(l.Arguments)); m != nil {
			id, _ := strconv.Atoi(m[1])
			t.Kind, t.SteamAppID = TargetSteam, id
		}
		if !strings.EqualFold(filepath.Ext(target), ".exe") && t.Kind == TargetExe {
			return t, fmt.Errorf("this shortcut points at %q, which is not an .exe", filepath.Base(target))
		}
		return t, nil
	case ".url":
		b, err := os.ReadFile(path)
		if err != nil {
			return Target{}, err
		}
		raw, icon, err := ParseURL(b)
		if err != nil {
			return Target{}, err
		}
		t := ClassifyURL(raw)
		t.Name, t.IconPath = name, icon
		return t, nil
	}
	return Target{}, fmt.Errorf("unsupported file type %q", filepath.Ext(path))
}
