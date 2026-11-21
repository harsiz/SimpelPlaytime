// Package detect decides what a running process is: a known game, a game
// found through a launcher manifest, something in a game folder, a normal
// app, or nothing worth tracking.
package detect

import (
	"os"
	"path"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/harsiz/SimpelPlaytime/internal/gamedb"
	"github.com/harsiz/SimpelPlaytime/internal/launchers"
	"github.com/harsiz/SimpelPlaytime/internal/model"
	"github.com/harsiz/SimpelPlaytime/internal/procs"
	"github.com/harsiz/SimpelPlaytime/internal/settings"
)

// Identity is what a process maps to.
type Identity struct {
	Key        string
	Name       string
	Kind       model.Kind
	Source     model.Source
	SteamAppID int
	Wiki       string
}

// LauncherIndex looks up exe paths in launcher manifests (Steam, Epic).
type LauncherIndex interface {
	Lookup(exePath string) (launchers.Game, bool)
}

// Options wires the detector up.
type Options struct {
	DB        *gamedb.DB
	Launchers LauncherIndex
	Settings  func() settings.Settings
	// Entries returns the library, used for custom matchers.
	Entries func() []model.Entry
	// ExeInfo returns version info strings for an exe (may be nil).
	ExeInfo func(path string) (product, description string)
	SelfPID uint32
	// SystemDirs are normalized (lower case, backslash) folders whose
	// programs are never tracked as apps. Defaults to the Windows folder.
	SystemDirs []string
}

type customRule struct {
	key    string
	typ    string
	value  string // normalized
	title  *regexp.Regexp
	weight int
}

type result struct {
	id Identity
	ok bool
}

// Detector maps processes to identities. It is safe for concurrent use.
type Detector struct {
	opts Options

	mu          sync.Mutex
	custom      []customRule
	customExes  map[string]bool // exes that have custom title rules
	customDirty bool
	cache       map[string]result
	names       map[string]string
}

// New makes a detector.
func New(opts Options) *Detector {
	if opts.DB == nil {
		opts.DB = gamedb.MustDefault()
	}
	if opts.Settings == nil {
		opts.Settings = settings.Defaults
	}
	if opts.SystemDirs == nil {
		opts.SystemDirs = DefaultSystemDirs()
	}
	return &Detector{opts: opts, customDirty: true, cache: map[string]result{}, names: map[string]string{}}
}

// DefaultSystemDirs returns the folders of the OS itself.
func DefaultSystemDirs() []string {
	if runtime.GOOS == "windows" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		return []string{withSlash(gamedb.NormPath(root))}
	}
	return []string{`\usr\sbin\`, `\sbin\`, `\usr\lib\systemd\`, `\lib\systemd\`, `\usr\libexec\`}
}

func withSlash(p string) string {
	if !strings.HasSuffix(p, `\`) {
		p += `\`
	}
	return p
}

// Invalidate drops caches. Call it when the library matchers or the settings
// change.
func (d *Detector) Invalidate() {
	d.mu.Lock()
	d.customDirty = true
	d.cache = map[string]result{}
	d.mu.Unlock()
}

func (d *Detector) rebuildCustomLocked() {
	d.custom = d.custom[:0]
	d.customExes = map[string]bool{}
	if d.opts.Entries == nil {
		d.customDirty = false
		return
	}
	for _, e := range d.opts.Entries() {
		for _, m := range e.Matchers {
			r := customRule{key: e.Key, typ: m.Type}
			switch m.Type {
			case model.MatchPath:
				r.value = gamedb.NormPath(strings.TrimSpace(m.Value))
				r.weight = 3000
			case model.MatchFolder:
				r.value = withSlash(gamedb.NormPath(strings.TrimSpace(m.Value)))
				r.weight = 2000 + len(r.value)
			case model.MatchExe:
				r.value = strings.ToLower(strings.TrimSpace(m.Value))
				r.weight = 1000
				if m.Title != "" {
					re, err := regexp.Compile("(?i)" + m.Title)
					if err != nil {
						continue
					}
					r.title = re
					r.weight = 1500
					d.customExes[r.value] = true
				}
			default:
				continue
			}
			if r.value == "" || r.value == `\` {
				continue
			}
			d.custom = append(d.custom, r)
		}
	}
	sort.SliceStable(d.custom, func(i, j int) bool { return d.custom[i].weight > d.custom[j].weight })
	d.customDirty = false
}

func (r customRule) match(lname, np, title string) bool {
	switch r.typ {
	case model.MatchPath:
		return np != "" && np == r.value
	case model.MatchFolder:
		return np != "" && strings.HasPrefix(np, r.value)
	case model.MatchExe:
		if lname != r.value {
			return false
		}
		return r.title == nil || r.title.MatchString(title)
	}
	return false
}

// Identify maps a process to an identity. ok is false when the process should
// not be tracked.
func (d *Detector) Identify(p procs.Process) (Identity, bool) {
	name := p.Name
	if name == "" && p.Path != "" {
		name = procs.ExeName(p.Path)
	}
	if name == "" {
		return Identity{}, false
	}
	if d.opts.SelfPID != 0 && p.PID == d.opts.SelfPID {
		return Identity{}, false
	}
	lname := strings.ToLower(name)
	np := gamedb.NormPath(p.Path)

	d.mu.Lock()
	if d.customDirty {
		d.rebuildCustomLocked()
	}
	titleMatters := d.customExes[lname] || d.opts.DB.NeedsTitle(lname)
	ck := lname + "\x00" + np + "\x00" + strconv.FormatBool(p.HasWindow)
	if titleMatters {
		ck += "\x00" + p.Title
	}
	if r, ok := d.cache[ck]; ok {
		d.mu.Unlock()
		return r.id, r.ok
	}
	custom := d.custom
	d.mu.Unlock()

	id, ok := d.identify(p, name, lname, np, custom)

	d.mu.Lock()
	if len(d.cache) > 4096 {
		d.cache = map[string]result{}
	}
	d.cache[ck] = result{id, ok}
	d.mu.Unlock()
	return id, ok
}

func (d *Detector) identify(p procs.Process, name, lname, np string, custom []customRule) (Identity, bool) {
	st := d.opts.Settings()
	if builtinIgnored(lname) || st.IsIgnored(name) {
		return Identity{}, false
	}

	// 1. the user's own rules always win
	for _, r := range custom {
		if r.match(lname, np, p.Title) {
			return Identity{Key: r.key, Source: model.SourceCustom}, true
		}
	}

	// 2. the built in database
	if e := d.opts.DB.Match(name, p.Path, p.Title); e != nil {
		id := Identity{Key: "db:" + e.ID, Name: e.Name, Kind: e.Kind, Source: model.SourceDatabase, SteamAppID: e.SteamID, Wiki: e.Wiki}
		if e.Kind == model.KindGame {
			return id, true
		}
		if d.appAllowed(st, p, np) {
			return id, true
		}
		return Identity{}, false
	}

	excludedHelper := helperExe(lname)

	// 3. launcher manifests (Steam / Epic)
	if d.opts.Launchers != nil && p.Path != "" && !excludedHelper && !excludedFolder(np) {
		if g, ok := d.opts.Launchers.Lookup(p.Path); ok {
			if g.SteamAppID != 0 {
				if e := d.opts.DB.BySteamID(g.SteamAppID); e != nil {
					return Identity{Key: "db:" + e.ID, Name: e.Name, Kind: e.Kind, Source: model.SourceDatabase, SteamAppID: e.SteamID, Wiki: e.Wiki}, true
				}
				return Identity{Key: "steam:" + strconv.Itoa(g.SteamAppID), Name: g.Name, Kind: model.KindGame, Source: model.SourceSteam, SteamAppID: g.SteamAppID}, true
			}
			if g.ID != "" {
				return Identity{Key: g.Store + ":" + strings.ToLower(g.ID), Name: g.Name, Kind: model.KindGame, Source: sourceFor(g.Store)}, true
			}
		}
	}

	// 4. anything living in a game library folder is probably a game
	if folder, ok := GameFolder(p.Path); ok && !excludedHelper {
		return Identity{
			Key:    "game:" + gamedb.Slug(folder),
			Name:   d.friendlyName(p.Path, name, folder, true),
			Kind:   model.KindGame,
			Source: model.SourceHeuristic,
		}, true
	}

	// 5. everything else is an app (if the mode allows it)
	if !d.appAllowed(st, p, np) {
		return Identity{}, false
	}
	return Identity{
		Key:    "exe:" + AppKey(lname, np),
		Name:   d.friendlyName(p.Path, name, "", false),
		Kind:   model.KindApp,
		Source: model.SourceAuto,
	}, true
}

func sourceFor(store string) model.Source {
	switch store {
	case "steam":
		return model.SourceSteam
	case "epic":
		return model.SourceEpic
	}
	return model.SourceHeuristic
}

func (d *Detector) appAllowed(st settings.Settings, p procs.Process, np string) bool {
	switch st.TrackMode {
	case settings.TrackGames:
		return false
	case settings.TrackWindowed:
		if !p.HasWindow {
			return false
		}
	case settings.TrackAll:
		if p.Path == "" && !p.HasWindow {
			return false
		}
	}
	for _, sys := range d.opts.SystemDirs {
		if np != "" && strings.HasPrefix(np, sys) {
			return false
		}
	}
	return true
}

func (d *Detector) friendlyName(fullPath, exe, folder string, game bool) string {
	d.mu.Lock()
	if n, ok := d.names[fullPath+"\x00"+exe]; ok {
		d.mu.Unlock()
		return n
	}
	d.mu.Unlock()
	var product, desc string
	if d.opts.ExeInfo != nil && fullPath != "" {
		product, desc = d.opts.ExeInfo(fullPath)
	}
	product, desc = cleanVersionString(product), cleanVersionString(desc)
	var n string
	switch {
	case game && folder != "":
		// for games the folder name ("Hades II") is usually nicer than the
		// engine stuffed version info, unless the product name is legit
		if product != "" && !genericProduct(product) {
			n = product
		} else {
			n = folder
		}
	case desc != "" && !genericProduct(desc):
		n = desc
	case product != "" && !genericProduct(product):
		n = product
	default:
		n = PrettyExeName(exe)
	}
	d.mu.Lock()
	if len(d.names) > 4096 {
		d.names = map[string]string{}
	}
	d.names[fullPath+"\x00"+exe] = n
	d.mu.Unlock()
	return n
}

func cleanVersionString(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\u00ae' || r == '\u2122' || r == '\u00a9' || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	return strings.Join(strings.Fields(s), " ")
}

var genericProducts = []string{
	"unrealgame", "unreal engine", "unrealengine", "unity", "unity player", "godot engine",
	"electron", "microsoft windows operating system", "java platform se", "openjdk platform binary",
	"application", "launcher", "game", "bootstrapper", "nw.js", "cef", "chromium",
}

func genericProduct(s string) bool {
	l := strings.ToLower(s)
	for _, g := range genericProducts {
		if l == g || strings.HasPrefix(l, g+" ") {
			return true
		}
	}
	return len(l) < 2
}

// PrettyExeName turns "my_cool-app.exe" into "My cool app".
func PrettyExeName(exe string) string {
	n := strings.TrimSuffix(exe, path.Ext(exe))
	n = strings.NewReplacer("_", " ", "-", " ").Replace(n)
	n = strings.Join(strings.Fields(n), " ")
	if n == "" {
		return exe
	}
	r := []rune(n)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

var genericExes = map[string]bool{
	"java.exe": true, "javaw.exe": true, "python.exe": true, "pythonw.exe": true,
	"node.exe": true, "electron.exe": true, "launcher.exe": true, "game.exe": true,
	"start.exe": true, "client.exe": true, "main.exe": true, "app.exe": true,
	"application.exe": true, "run.exe": true, "play.exe": true, "nw.exe": true,
	"love.exe": true, "dosbox.exe": true, "unity.exe": true, "update.exe": true,
	"bootstrapper.exe": true, "win64.exe": true, "x64.exe": true, "bin.exe": true,
	"engine.exe": true, "player.exe": true, "editor.exe": true, "server.exe": true,
}

// AppKey builds the identity of an unknown app. Generic exe names get their
// parent folder mixed in so two different "launcher.exe" do not collide.
func AppKey(lname, normPath string) string {
	if !genericExes[lname] || normPath == "" {
		return lname
	}
	parts := strings.Split(strings.Trim(normPath, `\`), `\`)
	if len(parts) < 2 {
		return lname
	}
	parent := parts[len(parts)-2]
	if (parent == "bin" || parent == "win64" || parent == "x64" || parent == "binaries") && len(parts) >= 3 {
		parent = parts[len(parts)-3]
	}
	return lname + "@" + parent
}

var gameRoots = []string{
	`\steamapps\common\`, `\epic games\`, `\gog galaxy\games\`, `\gog games\`,
	`\xboxgames\`, `\riot games\`, `\ea games\`, `\origin games\`,
	`\ubisoft game launcher\games\`, `\amazon games\library\`, `\itch\apps\`,
	`\rockstar games\`, `\battlestate games\`, `\games\heroic\`, `\games\`,
}

var excludedFolders = []string{
	`\epic games\launcher\`, `\epic games\epic online services\`, `\rockstar games\launcher\`,
	`\rockstar games\social club\`, `\riot games\riot client\`, `\steamapps\common\steamworks shared\`,
	`\steamapps\common\steamvr\`, `\steamapps\common\steam controller configs\`,
	`\_commonredist\`, `\commonredist\`, `\redist\`, `\__installer\`, `\_installer\`,
	`\directx\`, `\vcredist\`, `\easyanticheat\`, `\battleye\`, `\steamapps\common\proton`,
	`\steamapps\common\steamlinuxruntime`,
}

func excludedFolder(np string) bool {
	for _, x := range excludedFolders {
		if strings.Contains(np, x) {
			return true
		}
	}
	return false
}

// GameFolder reports whether a path is inside a known game library folder
// and returns the game's own folder name in its original case.
func GameFolder(fullPath string) (string, bool) {
	if fullPath == "" {
		return "", false
	}
	orig := strings.ReplaceAll(fullPath, "/", `\`)
	np := strings.ToLower(orig)
	if len(np) != len(orig) {
		orig = np // unusual unicode, give up on keeping the case
	}
	if excludedFolder(np) {
		return "", false
	}
	for _, root := range gameRoots {
		i := strings.Index(np, root)
		if i < 0 {
			continue
		}
		if root == `\games\` && !atDriveRootOrHome(np[:i]) {
			continue
		}
		rest := orig[i+len(root):]
		j := strings.IndexByte(rest, '\\')
		if j <= 0 {
			continue // the exe sits directly in the root, not in a game folder
		}
		return rest[:j], true
	}
	return "", false
}

// atDriveRootOrHome limits the very generic "\games\" root to "D:\Games\" or
// "~/Games/" style folders.
func atDriveRootOrHome(prefix string) bool {
	if len(prefix) == 2 && prefix[1] == ':' {
		return true
	}
	parts := strings.Split(strings.Trim(prefix, `\`), `\`)
	return len(parts) == 2 && (parts[0] == "home" || parts[0] == "users") ||
		len(parts) == 3 && parts[1] == "users"
}

var helperGlobs = []string{
	"unins*.exe", "*crash*.exe", "*setup*.exe", "*redist*.exe", "*installer*.exe",
	"dxsetup.exe", "*updater*.exe", "easyanticheat*.exe", "battleye*.exe", "beservice*.exe",
	"*uninstall*.exe", "cefsharp*.exe", "*webhelper*.exe", "*report*.exe", "dotnet*.exe",
	"*helper.exe", "*service.exe", "*bugsplat*.exe", "*crs-*.exe", "vc*redist*.exe",
}

func helperExe(lname string) bool {
	for _, g := range helperGlobs {
		if ok, _ := path.Match(g, lname); ok {
			return true
		}
	}
	return false
}

var builtinIgnore = map[string]bool{
	"simpelplaytime.exe": true, "explorer.exe": true, "searchhost.exe": true, "searchapp.exe": true,
	"startmenuexperiencehost.exe": true, "shellexperiencehost.exe": true, "applicationframehost.exe": true,
	"textinputhost.exe": true, "systemsettings.exe": true, "lockapp.exe": true, "dwm.exe": true,
	"csrss.exe": true, "sihost.exe": true, "taskhostw.exe": true, "runtimebroker.exe": true,
	"ctfmon.exe": true, "securityhealthsystray.exe": true, "widgets.exe": true, "widgetservice.exe": true,
	"phoneexperiencehost.exe": true, "crossdeviceresume.exe": true, "msedgewebview2.exe": true,
	"conhost.exe": true, "openconsole.exe": true, "unitycrashhandler64.exe": true,
	"unitycrashhandler32.exe": true, "crashreportclient.exe": true, "easyanticheat.exe": true,
	"easyanticheat_eos.exe": true, "beservice.exe": true, "beservice_x64.exe": true,
	"steamerrorreporter.exe": true, "steamerrorreporter64.exe": true, "gameoverlayui.exe": true,
	"gameoverlayui64.exe": true, "steamservice.exe": true, "nvidia share.exe": true,
	"nvidia overlay.exe": true, "nvcontainer.exe": true, "gamebar.exe": true, "gamebarftserver.exe": true,
	"xboxgamebarwidgets.exe": true, "rundll32.exe": true, "dllhost.exe": true, "svchost.exe": true,
	"werfault.exe": true, "consent.exe": true, "screenclippinghost.exe": true, "smartscreen.exe": true,
	"sechealthui.exe": true, "useroobebroker.exe": true, "comppkgsrv.exe": true,
	"epicwebhelper.exe": true, "epiconlineservicesuihelper.exe": true, "cefsharp.browsersubprocess.exe": true,
	"qtwebengineprocess.exe": true, "crashpad_handler.exe": true, "searchui.exe": true,
	"cortana.exe": true, "lsass.exe": true, "winlogon.exe": true, "wininit.exe": true,
	"services.exe": true, "smss.exe": true, "fontdrvhost.exe": true, "audiodg.exe": true,
	"spoolsv.exe": true, "msmpeng.exe": true, "nissrv.exe": true, "searchindexer.exe": true,
	"wmiprvse.exe": true, "backgroundtaskhost.exe": true, "inputpersonalization.exe": true,
	"shellhost.exe": true, "startmenu.exe": true, "nvdisplay.container.exe": true,
	"systemd": true, "kthreadd": true, "init": true,
}

func builtinIgnored(lname string) bool { return builtinIgnore[lname] }

// IsBuiltinIgnored is exported for the UI (to explain why something is not
// tracked).
func IsBuiltinIgnored(exe string) bool { return builtinIgnored(strings.ToLower(exe)) }
