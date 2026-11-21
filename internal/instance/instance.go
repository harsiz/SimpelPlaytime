// Package instance makes sure only one Simpel Playtime runs per data folder,
// and lets a second launch ask the first one to show its window.
package instance

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/harsiz/SimpelPlaytime/internal/fsutil"
)

// ErrAlreadyRunning means another instance holds the lock.
var ErrAlreadyRunning = errors.New("simpel playtime is already running")

const infoFile = "instance.json"

type info struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

// Instance is the running app's claim on the data folder.
type Instance struct {
	dir    string
	unlock func()
	srv    *http.Server
	ln     net.Listener
}

// Acquire takes the single instance lock for dir.
func Acquire(dir string) (*Instance, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	unlock, err := lock(dir)
	if err != nil {
		return nil, err
	}
	return &Instance{dir: dir, unlock: unlock}, nil
}

// Serve starts the tiny control server that SignalShow talks to.
func (i *Instance) Serve(onShow func()) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	mux := http.NewServeMux()
	mux.HandleFunc("/show", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Instance-Token")), []byte(tok)) != 1 {
			http.Error(w, "nope", http.StatusForbidden)
			return
		}
		if onShow != nil {
			onShow()
		}
		w.WriteHeader(http.StatusNoContent)
	})
	i.ln = ln
	i.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	data, _ := json.Marshal(info{Port: ln.Addr().(*net.TCPAddr).Port, Token: tok, PID: os.Getpid()})
	if err := fsutil.WriteFileAtomic(filepath.Join(i.dir, infoFile), data, 0o600); err != nil {
		ln.Close()
		return err
	}
	go func() { _ = i.srv.Serve(ln) }()
	return nil
}

// Release stops the control server and frees the lock.
func (i *Instance) Release() {
	if i.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = i.srv.Shutdown(ctx)
		cancel()
		_ = os.Remove(filepath.Join(i.dir, infoFile))
	}
	if i.unlock != nil {
		i.unlock()
		i.unlock = nil
	}
}

// SignalShow asks the running instance to show its window.
func SignalShow(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, infoFile))
	if err != nil {
		return err
	}
	var inf info
	if err := json.Unmarshal(data, &inf); err != nil || inf.Port == 0 {
		return errors.New("bad instance info")
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/show", inf.Port), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Instance-Token", inf.Token)
	c := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("instance answered %s", resp.Status)
	}
	return nil
}
