package runtime

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lildocker/builder"
	"lildocker/image"
)

func setup(t *testing.T, ref, dockerfile string, files map[string]string) {
	t.Helper()
	ctx := t.TempDir()
	files["lilDockfile"] = dockerfile
	for name, body := range files {
		p := filepath.Join(ctx, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.BuildImage(ctx, ref); err != nil {
		t.Fatal(err)
	}
}

func home(t *testing.T) { t.Setenv("LILDOCKER_HOME", t.TempDir()) }

func run(t *testing.T, img string, o RunOptions) (string, int) {
	t.Helper()
	var out bytes.Buffer
	o.Stdout = &out
	code, err := Run(img, o)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return out.String(), code
}

func TestBuildAndRun(t *testing.T) {
	home(t)
	setup(t, "demo", "WORKDIR /app\nCOPY data.txt .\nENV WHO=tester\nCMD echo hello $WHO\n", map[string]string{"data.txt": "payload"})

	out, code := run(t, "demo", RunOptions{Name: "one"})
	if code != 0 || !strings.Contains(out, "hello tester") {
		t.Fatalf("code %d output %q", code, out)
	}
	c, err := Find("one")
	if err != nil || c.Status != StatusExited {
		t.Fatalf("find: %+v %v", c, err)
	}
	if data, _ := os.ReadFile(filepath.Join(c.Rootfs(), "app", "data.txt")); string(data) != "payload" {
		t.Errorf("copied file: %q", data)
	}
	var logs bytes.Buffer
	Logs("one", false, -1, &logs)
	if logs.String() != out {
		t.Errorf("logs %q differ from output %q", logs.String(), out)
	}
	logs.Reset()
	Logs("one", false, 1, &logs)
	if logs.String() != out {
		t.Errorf("tail 1 of a one-line log should be the same line, got %q", logs.String())
	}
}

func TestExitCodeEnvAndOverride(t *testing.T) {
	home(t)
	setup(t, "demo:v2", "CMD echo default\n", map[string]string{"x": "1"})
	if _, code := run(t, "demo:v2", RunOptions{Args: []string{"sh", "-c", "exit 3"}}); code != 3 {
		t.Fatalf("want exit 3, got %d", code)
	}
	out, _ := run(t, "demo:v2", RunOptions{Args: []string{"sh", "-c", "echo $FOO $HOSTNAME"}, Env: []string{"FOO=bar"}, Hostname: "box"})
	if strings.TrimSpace(out) != "bar box" {
		t.Errorf("env/hostname not applied: %q", out)
	}
	out, _ = run(t, "demo:v2", RunOptions{Entrypoint: "echo", Args: []string{"via", "entrypoint"}})
	if strings.TrimSpace(out) != "via entrypoint" {
		t.Errorf("entrypoint override: %q", out)
	}
}

func TestRemoveFlagAndNames(t *testing.T) {
	home(t)
	setup(t, "demo", "CMD echo hi\n", map[string]string{"x": "1"})
	run(t, "demo", RunOptions{Name: "gone", Remove: true})
	if _, err := Find("gone"); err == nil {
		t.Fatal("container should have been removed")
	}
	run(t, "demo", RunOptions{Name: "keep"})
	if _, err := Create("demo", RunOptions{Name: "keep"}); err == nil {
		t.Fatal("duplicate names must be rejected")
	}
	if err := Rename("keep", "renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := Find("renamed"); err != nil {
		t.Fatal("rename did not stick")
	}
	if err := Remove("renamed", false); err != nil {
		t.Fatal(err)
	}
}

func TestVolumes(t *testing.T) {
	home(t)
	setup(t, "demo", "CMD echo hi\n", map[string]string{"x": "1"})
	host := t.TempDir()

	run(t, "demo", RunOptions{WorkDir: "/data", Volumes: []string{host + ":/data"}, Args: []string{"sh", "-c", "echo hello > out.txt"}})
	if b, _ := os.ReadFile(filepath.Join(host, "out.txt")); strings.TrimSpace(string(b)) != "hello" {
		t.Fatalf("bind mount did not reach the host folder, got %q", b)
	}

	run(t, "demo", RunOptions{WorkDir: "/store", Volumes: []string{"shared:/store"}, Args: []string{"sh", "-c", "echo kept > f"}})
	out, _ := run(t, "demo", RunOptions{WorkDir: "/store", Volumes: []string{"shared:/store"}, Args: []string{"sh", "-c", "cat f"}})
	if strings.TrimSpace(out) != "kept" {
		t.Fatalf("named volume lost its data: %q", out)
	}
	vs, _ := ListVolumes()
	if len(vs) != 1 || vs[0].Name != "shared" || len(vs[0].UsedBy) != 2 {
		t.Fatalf("volume list: %+v", vs)
	}
	if err := RemoveVolume("shared", false); err == nil {
		t.Error("a volume in use should not be removable")
	}
	cs, _ := List(true)
	for _, c := range cs {
		Remove(c.ID, false)
	}
	if gone, _ := PruneVolumes(); len(gone) != 1 {
		t.Errorf("prune should drop the unused volume, got %v", gone)
	}
	if _, err := os.Stat(filepath.Join(host, "out.txt")); err != nil {
		t.Error("removing containers must never touch bind-mounted host folders")
	}
}

func TestParseMount(t *testing.T) {
	d := t.TempDir()
	m, err := parseMount("/tmp:/x:ro", d, 0)
	if err != nil || !m.ReadOnly || m.Target != "/x" {
		t.Errorf("%+v %v", m, err)
	}
	if m, _ := parseMount("/anon", d, 3); !m.Anonymous || !strings.HasSuffix(m.Source, filepath.Join("volumes", "3")) {
		t.Errorf("anonymous volume: %+v", m)
	}
	for _, bad := range []string{"vol:relative", "a:b:c:d", "/tmp:/x:bogus"} {
		if _, err := parseMount(bad, d, 0); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	if got := splitSpec(`C:\data:/data:ro`); !reflect.DeepEqual(got, []string{`C:\data`, "/data", "ro"}) {
		t.Errorf("windows drive split: %q", got)
	}
}

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, p, _ := net.SplitHostPort(l.Addr().String())
	return p
}

func TestPortForward(t *testing.T) {
	app := freePort(t)
	host := freePort(t)
	l, err := net.Listen("tcp", "127.0.0.1:"+app)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()

	stop, err := startForwards([]string{"127.0.0.1:" + host + ":" + app + "/tcp"})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	c, err := net.Dial("tcp", "127.0.0.1:"+host)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("ping"))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo through the forward failed: %q %v", buf, err)
	}

	for _, bad := range []string{"80", "a:b", "80:80/udp", "0:80", "1:2:3:4"} {
		if _, err := parsePort(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestRestartPolicy(t *testing.T) {
	home(t)
	setup(t, "flaky", "CMD sh -c 'exit 1'\n", map[string]string{"x": "1"})
	_, code := run(t, "flaky", RunOptions{Name: "f", Restart: "on-failure:2"})
	c, _ := Find("f")
	if code != 1 || c.RestartCount != 2 {
		t.Fatalf("want 2 restarts then exit 1, got code %d restarts %d", code, c.RestartCount)
	}
	if _, err := Create("flaky", RunOptions{Restart: "sometimes"}); err == nil {
		t.Error("bad restart policy accepted")
	}
}

func TestCommitDiffCopyExport(t *testing.T) {
	home(t)
	setup(t, "demo", "CMD echo hi\n", map[string]string{"orig.txt": "o"})
	run(t, "demo", RunOptions{Name: "w", Args: []string{"sh", "-c", "echo new > created.txt && rm orig.txt"}})

	changes, err := Diff("w")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range changes {
		got[c.Path] = c.Kind
	}
	if got["/created.txt"] != "A" || got["/orig.txt"] != "D" {
		t.Errorf("diff: %v", got)
	}

	m, err := Commit("w", "snap:1")
	if err != nil || m.Ref() != "snap:1" {
		t.Fatalf("commit: %v", err)
	}
	dir := t.TempDir()
	loaded, _ := image.Load("snap:1")
	image.Extract(loaded, dir)
	if _, err := os.Stat(filepath.Join(dir, "created.txt")); err != nil {
		t.Error("committed image lacks the new file")
	}

	out := filepath.Join(t.TempDir(), "got.txt")
	if err := CopyFrom("w", "/created.txt", out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); strings.TrimSpace(string(b)) != "new" {
		t.Errorf("cp out: %q", b)
	}
	src := filepath.Join(t.TempDir(), "in.txt")
	os.WriteFile(src, []byte("inside"), 0o644)
	if err := CopyTo("w", src, "/dropped.txt"); err != nil {
		t.Fatal(err)
	}
	c, _ := Find("w")
	if b, _ := os.ReadFile(filepath.Join(c.Rootfs(), "dropped.txt")); string(b) != "inside" {
		t.Errorf("cp in: %q", b)
	}
	var tarBuf bytes.Buffer
	if err := Export("w", &tarBuf); err != nil || tarBuf.Len() == 0 {
		t.Errorf("export: %v", err)
	}
}

func TestResolveCommand(t *testing.T) {
	cases := []struct {
		m    image.Manifest
		args []string
		want []string
	}{
		{image.Manifest{Cmd: `["a","b"]`}, nil, []string{"a", "b"}},
		{image.Manifest{Cmd: `["a","b"]`}, []string{"x"}, []string{"x"}},
		{image.Manifest{Entrypoint: `["e"]`, Cmd: `["c"]`}, nil, []string{"e", "c"}},
		{image.Manifest{Entrypoint: `["e"]`, Cmd: `["c"]`}, []string{"z"}, []string{"e", "z"}},
		{image.Manifest{}, nil, nil},
	}
	for i, c := range cases {
		if got := resolveCommand(&c.m, c.args); !reflect.DeepEqual(got, c.want) {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
}
