package image

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func Pack(src string, w io.Writer) error { return PackSkip(src, w, nil) }

// PackSkip is Pack leaving out the given slash-separated relative paths.
func PackSkip(src string, w io.Writer, skip map[string]bool) error {
	tw := tar.NewWriter(w)

	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil || rel == "." {
			return err
		}
		slash := filepath.ToSlash(rel)
		if skipped(skip, slash) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}

		mode := info.Mode()
		link := ""
		switch {
		case mode&os.ModeSymlink != 0:
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		case !mode.IsRegular() && !mode.IsDir():
			return nil
		}

		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		h.Name = slash
		if mode.IsDir() {
			h.Name += "/"
		}
		h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""

		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if !mode.IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

func skipped(skip map[string]bool, rel string) bool {
	for k := range skip {
		if rel == k || strings.HasPrefix(rel, k+"/") {
			return true
		}
	}
	return false
}

// Unpack extracts a plain tar into dest.
func Unpack(r io.Reader, dest string) error { return extract(r, dest, false) }

// ApplyLayer extracts a tar (gzip is detected) honouring .wh. whiteout files.
func ApplyLayer(r io.Reader, dest string) error {
	br := bufio.NewReader(r)
	if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return err
		}
		defer zr.Close()
		return extract(zr, dest, true)
	}
	return extract(br, dest, true)
}

func extract(r io.Reader, dest string, layer bool) error {
	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}

	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		rel := path.Clean("/" + h.Name)
		if rel == "/" {
			continue
		}
		base := path.Base(rel)
		parent, err := ResolveInRoot(root, path.Dir(rel))
		if err != nil {
			return err
		}

		if layer && strings.HasPrefix(base, ".wh.") {
			if base == ".wh..wh..opq" {
				entries, _ := os.ReadDir(parent)
				for _, e := range entries {
					os.RemoveAll(filepath.Join(parent, e.Name()))
				}
			} else {
				os.RemoveAll(filepath.Join(parent, strings.TrimPrefix(base, ".wh.")))
			}
			continue
		}

		target := filepath.Join(parent, base)
		perm := fs.FileMode(h.Mode).Perm()

		switch h.Typeflag {
		case tar.TypeDir:
			real, err := ResolveInRoot(root, rel)
			if err != nil {
				return err
			}
			if err = os.MkdirAll(real, perm|0o700); err == nil {
				os.Chmod(real, perm|0o700)
			}
		case tar.TypeReg:
			if err = prepare(parent, target); err == nil {
				err = writeFile(target, tr, perm)
			}
		case tar.TypeSymlink:
			if err = prepare(parent, target); err == nil {
				err = os.Symlink(h.Linkname, target)
			}
		case tar.TypeLink:
			src, rerr := ResolveParent(root, path.Clean("/"+h.Linkname))
			if err = prepare(parent, target); err == nil && rerr == nil {
				if err = os.Link(src, target); err != nil {
					err = CopyFile(src, target, perm)
				}
			}
		}
		if err != nil {
			return fmt.Errorf("%s: %w", h.Name, err)
		}
	}
}

func prepare(parent, target string) error {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if st, err := os.Lstat(target); err == nil {
		if st.IsDir() {
			return os.RemoveAll(target)
		}
		return os.Remove(target)
	}
	return nil
}

func writeFile(p string, r io.Reader, perm fs.FileMode) error {
	out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(p, perm|0o200)
}

// ResolveInRoot resolves p as if root were "/", following symlinks (absolute
// ones included) without ever leaving root. The result need not exist.
func ResolveInRoot(root, p string) (string, error) {
	queue := splitPath(p)
	cur := root
	hops := 0
	for len(queue) > 0 {
		part := queue[0]
		queue = queue[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if cur != root {
				cur = filepath.Dir(cur)
			}
			continue
		}
		next := filepath.Join(cur, part)
		st, err := os.Lstat(next)
		if err != nil || st.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		if hops++; hops > 64 {
			return "", errors.New("too many symbolic links")
		}
		link, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(link, "/") {
			cur = root
		}
		queue = append(splitPath(link), queue...)
	}
	return cur, nil
}

// ResolveParent resolves everything but the last element of p.
func ResolveParent(root, p string) (string, error) {
	p = path.Clean("/" + filepath.ToSlash(p))
	if p == "/" {
		return root, nil
	}
	dir, err := ResolveInRoot(root, path.Dir(p))
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, path.Base(p)), nil
}

func splitPath(p string) []string {
	return strings.Split(strings.ReplaceAll(p, "\\", "/"), "/")
}

func CopyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeFile(dst, in, mode.Perm())
}

// CopyTree copies the contents of src into dst. skip may leave entries out.
func CopyTree(src, dst string, skip func(p string, d fs.DirEntry) bool) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skip != nil && skip(p, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(out, 0o755)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			os.Remove(out)
			return os.Symlink(link, out)
		case info.Mode().IsRegular():
			return CopyFile(p, out, info.Mode())
		}
		return nil
	})
}
