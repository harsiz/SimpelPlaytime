// Package art downloads posters, hero banners and metadata for games.
//
// Sources, in order:
//  1. Steam (no key needed): the library capsule (600x900), the library hero
//     and the store details (year, genres, developers). Games without a known
//     Steam app id are looked up through the Steam store search, but only an
//     exact name match is accepted so we never show the wrong poster.
//  2. SteamGridDB, when the user pasted an API key in the settings.
//  3. Wikipedia page images (for big non Steam games like Roblox or osu!).
//
// Everything is cached on disk. When nothing is found the UI draws a nice
// generated poster from the game's icon instead.
package art

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/harsiz/SimpelPlaytime/internal/fsutil"
	"github.com/harsiz/SimpelPlaytime/internal/model"
)

// ErrNotFound means no source had art for the game.
var ErrNotFound = errors.New("no artwork found")

// Poster and hero sizes stored on disk (small on purpose, the app should
// stay light on RAM).
const (
	PosterW = 400
	PosterH = 600
	HeroW   = 1280
)

const maxBody = 15 << 20

// Endpoints lets tests point the fetcher at fake servers.
type Endpoints struct {
	StoreSearch string
	AppDetails  string
	// CDN holds URL templates with %d for the app id and %s for the file.
	CDN         []string
	Wikipedia   string
	SteamGridDB string
}

// DefaultEndpoints are the real services.
func DefaultEndpoints() Endpoints {
	return Endpoints{
		StoreSearch: "https://store.steampowered.com/api/storesearch/",
		AppDetails:  "https://store.steampowered.com/api/appdetails",
		CDN: []string{
			"https://shared.steamstatic.com/store_item_assets/steam/apps/%d/%s",
			"https://cdn.cloudflare.steamstatic.com/steam/apps/%d/%s",
		},
		Wikipedia:   "https://en.wikipedia.org/api/rest_v1/page/summary/",
		SteamGridDB: "https://www.steamgriddb.com/api/v2",
	}
}

// Request describes what to fetch.
type Request struct {
	Key        string
	Name       string
	Kind       model.Kind
	SteamAppID int
	Wiki       string
}

// Result is what was found.
type Result struct {
	SteamAppID int
	PosterFile string // file name inside the art dir
	HeroFile   string
	Meta       *model.Meta
	Source     string
}

// Fetcher downloads art.
type Fetcher struct {
	Client       *http.Client
	Endpoints    Endpoints
	Dir          string
	UserAgent    string
	SteamGridKey func() string
	MinGap       time.Duration

	mu   sync.Mutex
	last time.Time
}

// New makes a fetcher that saves into dir.
func New(dir, version string) *Fetcher {
	return &Fetcher{
		Client:    &http.Client{Timeout: 20 * time.Second},
		Endpoints: DefaultEndpoints(),
		Dir:       dir,
		UserAgent: "SimpelPlaytime/" + version + " (playtime tracker)",
		MinGap:    350 * time.Millisecond,
	}
}

func (f *Fetcher) wait(ctx context.Context) error {
	f.mu.Lock()
	next := f.last.Add(f.MinGap)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	f.last = next
	f.mu.Unlock()
	d := time.Until(next)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (f *Fetcher) get(ctx context.Context, u string, header map[string]string) ([]byte, int, error) {
	if err := f.wait(ctx); err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return body, resp.StatusCode, nil
}

func (f *Fetcher) getJSON(ctx context.Context, u string, header map[string]string, v any) error {
	body, _, err := f.get(ctx, u, header)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

func (f *Fetcher) getImage(ctx context.Context, u string, header map[string]string) (image.Image, error) {
	body, _, err := f.get(ctx, u, header)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	return img, err
}

// Normalize makes names comparable: lower case, letters and digits only,
// trademark symbols and punctuation dropped.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type searchResp struct {
	Items []struct {
		Type string `json:"type"`
		Name string `json:"name"`
		ID   int    `json:"id"`
	} `json:"items"`
}

// SearchSteam finds the app id of a game by exact (normalized) name.
func (f *Fetcher) SearchSteam(ctx context.Context, name string) (int, error) {
	q := url.Values{"term": {name}, "l": {"english"}, "cc": {"US"}}
	var r searchResp
	if err := f.getJSON(ctx, f.Endpoints.StoreSearch+"?"+q.Encode(), nil, &r); err != nil {
		return 0, err
	}
	want := Normalize(name)
	for _, it := range r.Items {
		if it.Type == "app" && Normalize(it.Name) == want {
			return it.ID, nil
		}
	}
	return 0, ErrNotFound
}

type appDetails struct {
	Success bool `json:"success"`
	Data    struct {
		Type             string `json:"type"`
		Name             string `json:"name"`
		ShortDescription string `json:"short_description"`
		HeaderImage      string `json:"header_image"`
		ReleaseDate      struct {
			ComingSoon bool   `json:"coming_soon"`
			Date       string `json:"date"`
		} `json:"release_date"`
		Genres []struct {
			Description string `json:"description"`
		} `json:"genres"`
		Developers []string `json:"developers"`
		Publishers []string `json:"publishers"`
	} `json:"data"`
}

// SteamDetails fetches metadata and the header image URL of an app.
func (f *Fetcher) SteamDetails(ctx context.Context, appID int) (*model.Meta, string, error) {
	q := url.Values{"appids": {strconv.Itoa(appID)}, "filters": {"basic,genres,release_date,developers,publishers"}, "l": {"english"}}
	var r map[string]appDetails
	if err := f.getJSON(ctx, f.Endpoints.AppDetails+"?"+q.Encode(), nil, &r); err != nil {
		return nil, "", err
	}
	d, ok := r[strconv.Itoa(appID)]
	if !ok || !d.Success {
		return nil, "", ErrNotFound
	}
	m := &model.Meta{
		ReleaseDate: d.Data.ReleaseDate.Date,
		Year:        yearOf(d.Data.ReleaseDate.Date),
		Description: strings.TrimSpace(d.Data.ShortDescription),
		Developers:  d.Data.Developers,
		Publishers:  d.Data.Publishers,
	}
	for _, g := range d.Data.Genres {
		if g.Description != "" {
			m.Genres = append(m.Genres, g.Description)
		}
	}
	return m, d.Data.HeaderImage, nil
}

// yearOf pulls a four digit year out of strings like "Sep 25, 2025".
func yearOf(s string) int {
	for i := 0; i+4 <= len(s); i++ {
		if y, err := strconv.Atoi(s[i : i+4]); err == nil && y >= 1970 && y <= 2100 {
			if (i == 0 || !unicode.IsDigit(rune(s[i-1]))) && (i+4 == len(s) || !unicode.IsDigit(rune(s[i+4]))) {
				return y
			}
		}
	}
	return 0
}

func (f *Fetcher) steamAsset(ctx context.Context, appID int, file string) (image.Image, error) {
	var lastErr error = ErrNotFound
	for _, tpl := range f.Endpoints.CDN {
		img, err := f.getImage(ctx, fmt.Sprintf(tpl, appID, file), nil)
		if err == nil {
			return img, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

type wikiSummary struct {
	Type          string `json:"type"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	OriginalImage *struct {
		Source string `json:"source"`
	} `json:"originalimage"`
	Thumbnail *struct {
		Source string `json:"source"`
	} `json:"thumbnail"`
}

// WikipediaImage returns the page image of an article about a game.
func (f *Fetcher) WikipediaImage(ctx context.Context, title string, mustBeGame bool) (image.Image, error) {
	var s wikiSummary
	u := f.Endpoints.Wikipedia + url.PathEscape(strings.ReplaceAll(title, " ", "_"))
	if err := f.getJSON(ctx, u, nil, &s); err != nil {
		return nil, err
	}
	if s.Type == "disambiguation" {
		return nil, ErrNotFound
	}
	if mustBeGame && !strings.Contains(strings.ToLower(s.Description), "game") {
		return nil, ErrNotFound
	}
	// the thumbnail is preferred: it is small and the image servers
	// rate limit full size originals for apps like this one
	src := ""
	if s.Thumbnail != nil {
		src = s.Thumbnail.Source
	} else if s.OriginalImage != nil {
		src = s.OriginalImage.Source
	}
	if src == "" || strings.HasSuffix(strings.ToLower(strings.SplitN(src, "?", 2)[0]), ".svg") {
		return nil, ErrNotFound
	}
	return f.getImage(ctx, src, nil)
}

type sgdbSearch struct {
	Success bool `json:"success"`
	Data    []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"data"`
}

type sgdbImages struct {
	Success bool `json:"success"`
	Data    []struct {
		URL string `json:"url"`
	} `json:"data"`
}

// SteamGridDB fetches a 600x900 grid and a hero from SteamGridDB.
func (f *Fetcher) SteamGridDB(ctx context.Context, key, name string) (poster, hero image.Image, err error) {
	h := map[string]string{"Authorization": "Bearer " + key}
	var s sgdbSearch
	if err := f.getJSON(ctx, f.Endpoints.SteamGridDB+"/search/autocomplete/"+url.PathEscape(name), h, &s); err != nil {
		return nil, nil, err
	}
	if !s.Success || len(s.Data) == 0 {
		return nil, nil, ErrNotFound
	}
	id := s.Data[0].ID
	for _, d := range s.Data {
		if Normalize(d.Name) == Normalize(name) {
			id = d.ID
			break
		}
	}
	var g sgdbImages
	if err := f.getJSON(ctx, fmt.Sprintf("%s/grids/game/%d?dimensions=600x900&types=static", f.Endpoints.SteamGridDB, id), h, &g); err != nil {
		return nil, nil, err
	}
	if len(g.Data) == 0 {
		return nil, nil, ErrNotFound
	}
	poster, err = f.getImage(ctx, g.Data[0].URL, nil)
	if err != nil {
		return nil, nil, err
	}
	var hr sgdbImages
	if err := f.getJSON(ctx, fmt.Sprintf("%s/heroes/game/%d?types=static", f.Endpoints.SteamGridDB, id), h, &hr); err == nil && len(hr.Data) > 0 {
		hero, _ = f.getImage(ctx, hr.Data[0].URL, nil)
	}
	return poster, hero, nil
}

// Fetch runs the whole pipeline and saves what it finds.
func (f *Fetcher) Fetch(ctx context.Context, req Request) (Result, error) {
	res := Result{SteamAppID: req.SteamAppID}
	var poster, hero image.Image
	isGame := req.Kind == model.KindGame

	if res.SteamAppID == 0 && isGame && req.Name != "" {
		if id, err := f.SearchSteam(ctx, req.Name); err == nil {
			res.SteamAppID = id
		} else if ctx.Err() != nil {
			return res, ctx.Err()
		}
	}
	if res.SteamAppID != 0 {
		meta, header, err := f.SteamDetails(ctx, res.SteamAppID)
		if err == nil {
			res.Meta = meta
		}
		if img, err := f.steamAsset(ctx, res.SteamAppID, "library_600x900.jpg"); err == nil {
			poster = img
		} else if header != "" {
			if img, err := f.getImage(ctx, header, nil); err == nil {
				poster = PortraitFrom(img)
			}
		}
		if img, err := f.steamAsset(ctx, res.SteamAppID, "library_hero.jpg"); err == nil {
			hero = img
		}
		if poster != nil {
			res.Source = "steam"
		}
	}
	if poster == nil && f.SteamGridKey != nil {
		if key := strings.TrimSpace(f.SteamGridKey()); key != "" && req.Name != "" {
			if p, h, err := f.SteamGridDB(ctx, key, req.Name); err == nil {
				poster = p
				if hero == nil {
					hero = h
				}
				res.Source = "steamgriddb"
			}
		}
	}
	if poster == nil && (isGame || req.Wiki != "") {
		title := req.Wiki
		if title == "" {
			title = req.Name
		}
		if title != "" {
			if img, err := f.WikipediaImage(ctx, title, req.Wiki == ""); err == nil {
				poster = PortraitFrom(img)
				res.Source = "wikipedia"
			}
		}
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if poster == nil && hero == nil {
		if res.Meta != nil {
			return res, nil
		}
		return res, ErrNotFound
	}
	base := fsutil.SafeName(req.Key)
	if poster != nil {
		name := base + "_poster.jpg"
		if err := SaveJPEG(filepath.Join(f.Dir, name), Cover(poster, PosterW, PosterH)); err != nil {
			return res, err
		}
		res.PosterFile = name
	}
	if hero != nil {
		name := base + "_hero.jpg"
		if err := SaveJPEG(filepath.Join(f.Dir, name), FitWidth(hero, HeroW)); err != nil {
			return res, err
		}
		res.HeroFile = name
	}
	return res, nil
}

// SaveJPEG writes an image as a JPEG file.
func SaveJPEG(path string, img image.Image) error {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, buf.Bytes(), 0o644)
}

// Cover scales and crops img to exactly w x h.
func Cover(img image.Image, w, h int) image.Image {
	b := img.Bounds()
	sw, sh := float64(b.Dx()), float64(b.Dy())
	scale := float64(w) / sw
	if sh*scale < float64(h) {
		scale = float64(h) / sh
	}
	cw, ch := int(float64(w)/scale), int(float64(h)/scale)
	x0 := b.Min.X + (b.Dx()-cw)/2
	y0 := b.Min.Y + (b.Dy()-ch)/2
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, image.Rect(x0, y0, x0+cw, y0+ch), xdraw.Src, nil)
	return dst
}

// FitWidth scales img down to at most w pixels wide.
func FitWidth(img image.Image, w int) image.Image {
	b := img.Bounds()
	if b.Dx() <= w {
		return img
	}
	h := b.Dy() * w / b.Dx()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}

// PortraitFrom turns a landscape or square image (a Steam header, a logo)
// into a 2:3 poster: a blurred, darkened copy fills the background and the
// original sits sharp in the middle.
func PortraitFrom(img image.Image) image.Image {
	bg := Cover(img, PosterW/4, PosterH/4)
	bg = BoxBlur(bg, 4)
	dst := image.NewRGBA(image.Rect(0, 0, PosterW, PosterH))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), bg, bg.Bounds(), xdraw.Src, nil)
	darken(dst, 0.45)
	b := img.Bounds()
	fw := PosterW - 40
	fh := b.Dy() * fw / b.Dx()
	if fh > PosterH-80 {
		fh = PosterH - 80
		fw = b.Dx() * fh / b.Dy()
	}
	x0 := (PosterW - fw) / 2
	y0 := (PosterH - fh) / 2
	xdraw.CatmullRom.Scale(dst, image.Rect(x0, y0, x0+fw, y0+fh), img, b, xdraw.Over, nil)
	return dst
}

func darken(img *image.RGBA, amount float64) {
	k := 1 - amount
	for i := 0; i+3 < len(img.Pix); i += 4 {
		img.Pix[i] = uint8(float64(img.Pix[i]) * k)
		img.Pix[i+1] = uint8(float64(img.Pix[i+1]) * k)
		img.Pix[i+2] = uint8(float64(img.Pix[i+2]) * k)
	}
}

// BoxBlur is a small, fast blur (three passes approximate a gaussian).
func BoxBlur(img image.Image, radius int) *image.RGBA {
	b := img.Bounds()
	src := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	xdraw.Draw(src, src.Bounds(), img, b.Min, xdraw.Src)
	if radius < 1 {
		return src
	}
	tmp := image.NewRGBA(src.Bounds())
	for pass := 0; pass < 3; pass++ {
		blurPass(src, tmp, radius, true)
		blurPass(tmp, src, radius, false)
	}
	return src
}

func blurPass(src, dst *image.RGBA, r int, horizontal bool) {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	outer, inner := h, w
	if !horizontal {
		outer, inner = w, h
	}
	for o := 0; o < outer; o++ {
		var sum [4]int
		at := func(i int) int {
			if i < 0 {
				i = 0
			}
			if i >= inner {
				i = inner - 1
			}
			if horizontal {
				return o*src.Stride + i*4
			}
			return i*src.Stride + o*4
		}
		for i := -r; i <= r; i++ {
			p := at(i)
			for c := 0; c < 4; c++ {
				sum[c] += int(src.Pix[p+c])
			}
		}
		n := 2*r + 1
		for i := 0; i < inner; i++ {
			var d int
			if horizontal {
				d = o*dst.Stride + i*4
			} else {
				d = i*dst.Stride + o*4
			}
			for c := 0; c < 4; c++ {
				dst.Pix[d+c] = uint8(sum[c] / n)
			}
			add, rem := at(i+r+1), at(i-r)
			for c := 0; c < 4; c++ {
				sum[c] += int(src.Pix[add+c]) - int(src.Pix[rem+c])
			}
		}
	}
}

// Average returns the average colour of an image (used to tint fallback
// posters and cards).
func Average(img image.Image) color.NRGBA {
	small := image.NewRGBA(image.Rect(0, 0, 8, 8))
	xdraw.ApproxBiLinear.Scale(small, small.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	var r, g, b, n int
	for i := 0; i+3 < len(small.Pix); i += 4 {
		if small.Pix[i+3] < 64 {
			continue
		}
		r += int(small.Pix[i])
		g += int(small.Pix[i+1])
		b += int(small.Pix[i+2])
		n++
	}
	if n == 0 {
		return color.NRGBA{R: 120, G: 80, B: 200, A: 255}
	}
	return color.NRGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(b / n), A: 255}
}
