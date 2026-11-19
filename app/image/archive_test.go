package image

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPackUnpack(t *testing.T) {
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(src, "a", "b", "f.txt"), []byte("hi"), 0o644)
	os.WriteFile(filepath.Join(src, "top.txt"), []byte("top"), 0o755)

	var buf bytes.Buffer
	if err := Pack(src, &buf); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := Unpack(&buf, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "a", "b", "f.txt"))
	if err != nil || string(got) != "hi" {
		t.Fatalf("nested file: %q %v", got, err)
	}
}

func TestUnpackStaysInsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: "../evil.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()

	if err := Unpack(&buf, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(parent, "evil.txt")); err == nil {
		t.Fatal("file escaped the root")
	}
	if _, err := os.Stat(filepath.Join(root, "evil.txt")); err != nil {
		t.Fatal("path should have been confined inside the root")
	}
}

func TestUnpackSymlinkCannotEscape(t *testing.T) {
	outside := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: "link", Linkname: outside, Typeflag: tar.TypeSymlink})
	tw.WriteHeader(&tar.Header{Name: "link/evil.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()

	if err := Unpack(&buf, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "evil.txt")); err == nil {
		t.Fatal("file was written through a symlink outside the root")
	}
}
