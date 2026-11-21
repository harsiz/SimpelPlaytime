package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "a.json")
	if err := WriteFileAtomic(p, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "two" {
		t.Fatalf("got %q, %v", got, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
	if !Exists(p) || Exists(p+".nope") {
		t.Error("Exists is wrong")
	}
}

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"db:hades-ii":   "db_hades-ii",
		"exe:osu!.exe":  "exe_osu_.exe",
		"":              "_",
		"...":           "_",
		`steam:1145350`: "steam_1145350",
		"a/b\\c":        "a_b_c",
	}
	for in, want := range cases {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}
