package art

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/harsiz/SimpelPlaytime/internal/model"
)

func solid(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

func jpg(w, h int, c color.RGBA) []byte {
	var b bytes.Buffer
	jpeg.Encode(&b, solid(w, h, c), nil)
	return b.Bytes()
}

func pngData(w, h int) []byte {
	var b bytes.Buffer
	png.Encode(&b, solid(w, h, color.RGBA{0, 200, 0, 255}))
	return b.Bytes()
}

type fake struct {
	srv      *httptest.Server
	requests atomic.Int64
	sgdbAuth atomic.Value
}

func newFake(t *testing.T) *fake {
	f := &fake{}
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		term := r.URL.Query().Get("term")
		items := []map[string]any{{"type": "app", "name": term + " Soundtrack", "id": 999}}
		if strings.EqualFold(term, "hades ii") {
			items = append(items, map[string]any{"type": "app", "name": "Hades II", "id": 100})
		}
		json.NewEncoder(w).Encode(map[string]any{"total": len(items), "items": items})
	})
	mux.HandleFunc("/details", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("appids")
		json.NewEncoder(w).Encode(map[string]any{id: map[string]any{"success": true, "data": map[string]any{
			"type": "game", "name": "X", "short_description": " A game. ", "header_image": f.srv.URL + "/img/header.jpg",
			"release_date": map[string]any{"coming_soon": false, "date": "Sep 25, 2025"},
			"genres":       []map[string]string{{"description": "Action"}, {"description": "RPG"}},
			"developers":   []string{"Supergiant Games"},
		}}})
	})
	mux.HandleFunc("/cdn/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/cdn/100/") {
			if strings.HasSuffix(r.URL.Path, "library_600x900.jpg") {
				w.Write(jpg(600, 900, color.RGBA{200, 0, 0, 255}))
				return
			}
			if strings.HasSuffix(r.URL.Path, "library_hero.jpg") {
				w.Write(jpg(1920, 620, color.RGBA{0, 0, 200, 255}))
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/img/header.jpg", func(w http.ResponseWriter, r *http.Request) { w.Write(jpg(460, 215, color.RGBA{10, 200, 10, 255})) })
	mux.HandleFunc("/img/logo.png", func(w http.ResponseWriter, r *http.Request) { w.Write(pngData(512, 512)) })
	mux.HandleFunc("/wiki/", func(w http.ResponseWriter, r *http.Request) {
		title := strings.TrimPrefix(r.URL.Path, "/wiki/")
		desc := "2006 online game platform"
		if title == "Banana" {
			desc = "Edible fruit"
		}
		json.NewEncoder(w).Encode(map[string]any{"type": "standard", "title": title, "description": desc,
			"originalimage": map[string]any{"source": f.srv.URL + "/img/logo.png"}})
	})
	mux.HandleFunc("/sgdb/", func(w http.ResponseWriter, r *http.Request) {
		f.sgdbAuth.Store(r.Header.Get("Authorization"))
		switch {
		case strings.HasPrefix(r.URL.Path, "/sgdb/search/autocomplete/"):
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []map[string]any{{"id": 5, "name": "Obscure Game"}}})
		case strings.HasPrefix(r.URL.Path, "/sgdb/grids/game/5"):
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []map[string]any{{"url": f.srv.URL + "/img/header.jpg"}}})
		default:
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []any{}})
		}
	})
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) fetcher(t *testing.T) *Fetcher {
	ft := New(t.TempDir(), "test")
	ft.MinGap = 0
	ft.Endpoints = Endpoints{
		StoreSearch: f.srv.URL + "/search",
		AppDetails:  f.srv.URL + "/details",
		CDN:         []string{f.srv.URL + "/cdn/%d/%s"},
		Wikipedia:   f.srv.URL + "/wiki/",
		SteamGridDB: f.srv.URL + "/sgdb",
	}
	return ft
}

func imageSize(t *testing.T, path string) (int, int) {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	cfg, _, err := image.DecodeConfig(fh)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height
}

func TestFetchSteamBySearch(t *testing.T) {
	f := newFake(t)
	ft := f.fetcher(t)
	res, err := ft.Fetch(context.Background(), Request{Key: "db:hades-ii", Name: "Hades II", Kind: model.KindGame})
	if err != nil {
		t.Fatal(err)
	}
	if res.SteamAppID != 100 || res.Source != "steam" || res.PosterFile == "" || res.HeroFile == "" {
		t.Fatalf("res = %+v", res)
	}
	if res.Meta == nil || res.Meta.Year != 2025 || len(res.Meta.Genres) != 2 || res.Meta.Description != "A game." {
		t.Errorf("meta = %+v", res.Meta)
	}
	if w, h := imageSize(t, filepath.Join(ft.Dir, res.PosterFile)); w != PosterW || h != PosterH {
		t.Errorf("poster %dx%d", w, h)
	}
	if w, _ := imageSize(t, filepath.Join(ft.Dir, res.HeroFile)); w != HeroW {
		t.Errorf("hero width %d", w)
	}
}

func TestFetchHeaderFallback(t *testing.T) {
	f := newFake(t)
	ft := f.fetcher(t)
	res, err := ft.Fetch(context.Background(), Request{Key: "steam:200", Name: "Whatever", Kind: model.KindGame, SteamAppID: 200})
	if err != nil {
		t.Fatal(err)
	}
	if res.PosterFile == "" || res.HeroFile != "" {
		t.Fatalf("res = %+v", res)
	}
	if w, h := imageSize(t, filepath.Join(ft.Dir, res.PosterFile)); w != PosterW || h != PosterH {
		t.Errorf("poster %dx%d", w, h)
	}
}

func TestFetchWikipediaFallback(t *testing.T) {
	f := newFake(t)
	ft := f.fetcher(t)
	res, err := ft.Fetch(context.Background(), Request{Key: "db:roblox", Name: "Roblox", Kind: model.KindGame})
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "wikipedia" || res.SteamAppID != 0 || res.PosterFile == "" {
		t.Errorf("res = %+v", res)
	}
	// an article that is not about a game must not be used
	if _, err := ft.Fetch(context.Background(), Request{Key: "x", Name: "Banana", Kind: model.KindGame}); err != ErrNotFound {
		t.Errorf("banana err = %v", err)
	}
	// apps never hit the network unless a wiki title is set
	before := f.requests.Load()
	if _, err := ft.Fetch(context.Background(), Request{Key: "y", Name: "Some App", Kind: model.KindApp}); err != ErrNotFound {
		t.Errorf("app err = %v", err)
	}
	if f.requests.Load() != before {
		t.Error("apps should not trigger requests")
	}
}

func TestFetchSteamGridDB(t *testing.T) {
	f := newFake(t)
	ft := f.fetcher(t)
	ft.SteamGridKey = func() string { return "secret" }
	ft.Endpoints.Wikipedia = f.srv.URL + "/nowiki/"
	res, err := ft.Fetch(context.Background(), Request{Key: "z", Name: "Obscure Game", Kind: model.KindGame})
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "steamgriddb" || res.PosterFile == "" {
		t.Errorf("res = %+v", res)
	}
	if got := f.sgdbAuth.Load(); got != "Bearer secret" {
		t.Errorf("auth header = %v", got)
	}
}

func TestCancelled(t *testing.T) {
	f := newFake(t)
	ft := f.fetcher(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ft.Fetch(ctx, Request{Key: "a", Name: "Hades II", Kind: model.KindGame}); err == nil {
		t.Error("cancelled fetch should fail")
	}
}

func TestRateLimit(t *testing.T) {
	f := newFake(t)
	ft := f.fetcher(t)
	ft.MinGap = 30 * time.Millisecond
	start := time.Now()
	for i := 0; i < 4; i++ {
		ft.SearchSteam(context.Background(), "nothing")
	}
	if time.Since(start) < 80*time.Millisecond {
		t.Error("requests should be spaced out")
	}
}

func TestHelpers(t *testing.T) {
	if Normalize("Hades™ II: The  Return!") != "hadesiithereturn" {
		t.Errorf("normalize = %q", Normalize("Hades™ II: The  Return!"))
	}
	for in, want := range map[string]int{"Sep 25, 2025": 2025, "2017": 2017, "Coming soon": 0, "12345": 0, "Q1 1999": 1999} {
		if got := yearOf(in); got != want {
			t.Errorf("yearOf(%q) = %d, want %d", in, got, want)
		}
	}
	c := Cover(solid(1000, 200, color.RGBA{1, 2, 3, 255}), 400, 600)
	if c.Bounds().Dx() != 400 || c.Bounds().Dy() != 600 {
		t.Errorf("cover = %v", c.Bounds())
	}
	p := PortraitFrom(solid(460, 215, color.RGBA{200, 100, 50, 255}))
	if p.Bounds().Dx() != PosterW || p.Bounds().Dy() != PosterH {
		t.Errorf("portrait = %v", p.Bounds())
	}
	avg := Average(solid(20, 20, color.RGBA{100, 50, 25, 255}))
	if avg.R != 100 || avg.G != 50 || avg.B != 25 {
		t.Errorf("average = %v", avg)
	}
	if a := Average(image.NewRGBA(image.Rect(0, 0, 4, 4))); a.A != 255 {
		t.Error("transparent image should give the default colour")
	}
	if FitWidth(solid(100, 50, color.RGBA{}), 200).Bounds().Dx() != 100 {
		t.Error("FitWidth should not upscale")
	}
}
