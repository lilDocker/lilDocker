package registry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lildocker/image"
)

func layer(t *testing.T, files map[string]string) ([]byte, string) {
	t.Helper()
	var raw bytes.Buffer
	zw := gzip.NewWriter(&raw)
	tw := tar.NewWriter(zw)
	names := []string{}
	for n := range files {
		names = append(names, n)
	}
	for _, n := range names {
		body := files[n]
		h := &tar.Header{Name: n, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if strings.HasSuffix(n, "/") {
			h = &tar.Header{Name: n, Mode: 0o755, Typeflag: tar.TypeDir}
		}
		tw.WriteHeader(h)
		tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	sum := sha256.Sum256(raw.Bytes())
	return raw.Bytes(), "sha256:" + hex.EncodeToString(sum[:])
}

func TestPull(t *testing.T) {
	t.Setenv("LILDOCKER_HOME", t.TempDir())

	l1, d1 := layer(t, map[string]string{"etc/": "", "etc/os": "base", "etc/old": "gone soon", "app/": "", "app/keep": "k"})
	l2, d2 := layer(t, map[string]string{"etc/.wh.old": "", "etc/new": "added"})
	cfg, _ := json.Marshal(map[string]any{
		"architecture": runtime.GOARCH, "os": "linux",
		"config": map[string]any{
			"Env": []string{"A=1"}, "Cmd": []string{"run", "me"}, "Entrypoint": []string{"/bin/x"},
			"WorkingDir": "/app", "ExposedPorts": map[string]any{"80/tcp": map[string]any{}},
		},
	})
	cs := sha256.Sum256(cfg)
	cfgDigest := "sha256:" + hex.EncodeToString(cs[:])

	manifest, _ := json.Marshal(map[string]any{
		"mediaType": mtDocker,
		"config":    map[string]any{"digest": cfgDigest},
		"layers": []map[string]any{
			{"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip", "digest": d1, "size": len(l1)},
			{"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip", "digest": d2, "size": len(l2)},
		},
	})
	ms := sha256.Sum256(manifest)
	mDigest := "sha256:" + hex.EncodeToString(ms[:])
	index, _ := json.Marshal(map[string]any{
		"mediaType": mtIndexDocker,
		"manifests": []map[string]any{
			{"digest": "sha256:other", "platform": map[string]any{"os": "windows", "architecture": runtime.GOARCH}},
			{"digest": mDigest, "platform": map[string]any{"os": "linux", "architecture": runtime.GOARCH}},
		},
	})

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if r.URL.Query().Get("scope") != "repository:team/app:pull" {
				http.Error(w, "bad scope", 400)
				return
			}
			fmt.Fprint(w, `{"token":"secret"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="test"`, srv.URL))
			w.WriteHeader(401)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/v1"):
			w.Write(index)
		case strings.HasSuffix(r.URL.Path, "/manifests/"+mDigest):
			w.Write(manifest)
		case strings.HasSuffix(r.URL.Path, "/blobs/"+cfgDigest):
			w.Write(cfg)
		case strings.HasSuffix(r.URL.Path, "/blobs/"+d1):
			w.Write(l1)
		case strings.HasSuffix(r.URL.Path, "/blobs/"+d2):
			w.Write(l2)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	m, err := Pull(host+"/team/app:v1", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if m.OS != "linux" || m.WorkDir != "/app" || m.Cmd != `["run","me"]` || m.Entrypoint != `["/bin/x"]` {
		t.Fatalf("config not carried over: %+v", m)
	}
	if len(m.Expose) != 1 || m.Env[0] != "A=1" {
		t.Fatalf("env/expose wrong: %+v", m)
	}

	dir := t.TempDir()
	if err := image.Extract(m, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "etc", "old")); err == nil {
		t.Error("whiteout did not remove etc/old")
	}
	for _, f := range []string{"etc/os", "etc/new", "app/keep"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if _, err := image.Load(m.Name + ":v1"); err != nil {
		t.Errorf("image not stored under %s: %v", m.Name, err)
	}
}

func TestPullBadChecksum(t *testing.T) {
	t.Setenv("LILDOCKER_HOME", t.TempDir())
	l, _ := layer(t, map[string]string{"f": "x"})
	cfg := []byte(`{"os":"linux","config":{}}`)
	cs := sha256.Sum256(cfg)
	cd := "sha256:" + hex.EncodeToString(cs[:])
	manifest, _ := json.Marshal(map[string]any{
		"config": map[string]any{"digest": cd},
		"layers": []map[string]any{{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": "sha256:" + strings.Repeat("0", 64)}},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Write(manifest)
		case strings.HasSuffix(r.URL.Path, cd):
			w.Write(cfg)
		default:
			w.Write(l)
		}
	}))
	defer srv.Close()
	if _, err := Pull(strings.TrimPrefix(srv.URL, "http://")+"/x/y:1", io.Discard); err == nil {
		t.Fatal("expected a checksum error")
	}
}

func TestParse(t *testing.T) {
	cases := map[string][3]string{
		"alpine":                 {hub, "library/alpine", "alpine"},
		"alpine:3.19":            {hub, "library/alpine", "alpine"},
		"bitnami/redis":          {hub, "bitnami/redis", "bitnami/redis"},
		"localhost:5000/team/a":  {"localhost:5000", "team/a", "localhost_5000/team/a"},
		"ghcr.io/owner/tool:1.0": {"ghcr.io", "owner/tool", "ghcr.io/owner/tool"},
	}
	for in, want := range cases {
		r, err := parse(in)
		if err != nil || r.host != want[0] || r.repo != want[1] || r.name != want[2] {
			t.Errorf("%s -> %+v %v", in, r, err)
		}
	}
}
