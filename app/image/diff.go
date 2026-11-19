package image

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type Change struct {
	Kind string
	Path string
}

type entry struct {
	dir  bool
	link string
	sum  string
}

// Diff compares a container folder with the image it came from.
func Diff(m *Manifest, rootfs string, skip map[string]bool) ([]Change, error) {
	f, err := os.Open(TarPath(m))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	base := map[string]entry{}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := filepath.ToSlash(filepath.Clean(h.Name))
		e := entry{dir: h.Typeflag == tar.TypeDir, link: h.Linkname}
		if h.Typeflag == tar.TypeReg {
			hash := sha256.New()
			io.Copy(hash, tr)
			e.sum = hex.EncodeToString(hash.Sum(nil))
		}
		base[name] = e
	}

	var out []Change
	seen := map[string]bool{}
	err = filepath.WalkDir(rootfs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(rootfs, p)
		if rel == "." {
			return nil
		}
		name := filepath.ToSlash(rel)
		if skipped(skip, name) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		seen[name] = true

		cur := entry{dir: d.IsDir()}
		if d.Type()&fs.ModeSymlink != 0 {
			cur.link, _ = os.Readlink(p)
		} else if d.Type().IsRegular() {
			if data, err := os.Open(p); err == nil {
				hash := sha256.New()
				io.Copy(hash, data)
				data.Close()
				cur.sum = hex.EncodeToString(hash.Sum(nil))
			}
		}
		old, ok := base[name]
		switch {
		case !ok:
			out = append(out, Change{"A", "/" + name})
		case old != cur && !(old.dir && cur.dir):
			out = append(out, Change{"C", "/" + name})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for name := range base {
		if !seen[name] && !skipped(skip, name) {
			out = append(out, Change{"D", "/" + name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
