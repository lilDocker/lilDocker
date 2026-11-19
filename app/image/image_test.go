package image

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestParseRef(t *testing.T) {
	good := map[string][2]string{
		"app":         {"app", "latest"},
		"app:1.2":     {"app", "1.2"},
		"team/app:v1": {"team/app", "v1"},
	}
	for in, want := range good {
		n, tag, err := ParseRef(in)
		if err != nil || n != want[0] || tag != want[1] {
			t.Errorf("%s -> %s %s %v", in, n, tag, err)
		}
	}
	for _, bad := range []string{"", "App", "a b", "app:", "a//b", "app:bad tag"} {
		if _, _, err := ParseRef(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func fixture(t *testing.T, ref string, files map[string]string) *Manifest {
	t.Helper()
	dir := t.TempDir()
	for n, body := range files {
		p := filepath.Join(dir, n)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	name, tag, _ := ParseRef(ref)
	m := &Manifest{Name: name, Tag: tag, WorkDir: "/", Cmd: "echo hi"}
	if err := Commit(m, dir, nil); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTagLoadRemove(t *testing.T) {
	t.Setenv("LILDOCKER_HOME", t.TempDir())
	m := fixture(t, "app:1", map[string]string{"f": "x"})

	if err := Tag("app:1", "mirror/app:stable"); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"app:1", "mirror/app:stable", m.ID, m.ID[:5]} {
		got, err := Load(ref)
		if err != nil || got.ID != m.ID {
			t.Errorf("load %s: %v", ref, err)
		}
	}
	if _, err := Load("app:2"); err == nil {
		t.Error("unknown tag should fail")
	}
	if gone, err := Remove("app:1"); err != nil || len(gone) != 1 {
		t.Fatalf("remove one tag: %v %v", gone, err)
	}
	if _, err := Load("mirror/app:stable"); err != nil {
		t.Error("other tag should survive")
	}
	if gone, _ := Remove(m.ID); len(gone) != 1 {
		t.Errorf("removing by id should drop the remaining tag, got %v", gone)
	}
}

func TestSaveLoadAndImport(t *testing.T) {
	t.Setenv("LILDOCKER_HOME", t.TempDir())
	m := fixture(t, "app:1", map[string]string{"dir/f": "payload"})

	var buf bytes.Buffer
	if err := Archive("app:1", &buf); err != nil {
		t.Fatal(err)
	}
	Remove("app:1")
	got, err := Unarchive(&buf)
	if err != nil || got.ID != m.ID || got.Cmd != "echo hi" {
		t.Fatalf("unarchive: %+v %v", got, err)
	}
	dir := t.TempDir()
	if err := Extract(got, dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "dir", "f")); string(b) != "payload" {
		t.Errorf("content lost: %q", b)
	}

	var rootfs bytes.Buffer
	tw := tar.NewWriter(&rootfs)
	tw.WriteHeader(&tar.Header{Name: "hello", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("hi"))
	tw.Close()
	imp, err := Import(&rootfs, "imported:1")
	if err != nil || imp.Name != "imported" {
		t.Fatalf("import: %+v %v", imp, err)
	}
}

func TestDiff(t *testing.T) {
	t.Setenv("LILDOCKER_HOME", t.TempDir())
	m := fixture(t, "app:1", map[string]string{"keep": "1", "change": "old", "remove": "x"})
	dir := t.TempDir()
	Extract(m, dir)
	os.WriteFile(filepath.Join(dir, "change"), []byte("new"), 0o644)
	os.WriteFile(filepath.Join(dir, "added"), []byte("a"), 0o644)
	os.Remove(filepath.Join(dir, "remove"))

	got, err := Diff(m, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"/added": "A", "/change": "C", "/remove": "D"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, c := range got {
		if want[c.Path] != c.Kind {
			t.Errorf("unexpected %v", c)
		}
	}
}

func TestResolveInRootStaysInside(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "usr", "lib"), 0o755)
	os.Symlink("/usr/lib", filepath.Join(root, "lib"))
	os.Symlink("../../../../etc", filepath.Join(root, "usr", "escape"))

	got, _ := ResolveInRoot(root, "/lib/x")
	if got != filepath.Join(root, "usr", "lib", "x") {
		t.Errorf("absolute link not rebased: %s", got)
	}
	got, _ = ResolveInRoot(root, "/usr/escape/passwd")
	if rel, _ := filepath.Rel(root, got); rel == ".." || len(rel) > 2 && rel[:3] == "../" {
		t.Errorf("escaped the root: %s", got)
	}
}

func TestUnpackLinksAndWhiteouts(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(h *tar.Header, body string) {
		tw.WriteHeader(h)
		tw.Write([]byte(body))
	}
	add(&tar.Header{Name: "usr/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(&tar.Header{Name: "usr/bin/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(&tar.Header{Name: "bin", Typeflag: tar.TypeSymlink, Linkname: "/usr/bin"}, "")
	add(&tar.Header{Name: "bin/tool", Typeflag: tar.TypeReg, Mode: 0o755, Size: 1}, "t")
	add(&tar.Header{Name: "usr/bin/tool2", Typeflag: tar.TypeLink, Linkname: "usr/bin/tool"}, "")
	tw.Close()

	dir := t.TempDir()
	if err := ApplyLayer(&buf, dir); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"usr/bin/tool", "usr/bin/tool2"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
}
