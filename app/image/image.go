package image

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"lildocker/config"
)

type Manifest struct {
	Name       string            `json:"name"`
	Tag        string            `json:"tag"`
	ID         string            `json:"id"`
	Created    time.Time         `json:"created"`
	Size       int64             `json:"size"`
	OS         string            `json:"os,omitempty"`
	Arch       string            `json:"arch,omitempty"`
	Base       string            `json:"base,omitempty"`
	Env        []string          `json:"env,omitempty"`
	WorkDir    string            `json:"workdir"`
	Entrypoint string            `json:"entrypoint,omitempty"`
	Cmd        string            `json:"cmd,omitempty"`
	User       string            `json:"user,omitempty"`
	StopSignal string            `json:"stop_signal,omitempty"`
	Expose     []string          `json:"expose,omitempty"`
	Volumes    []string          `json:"volumes,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	History    []string          `json:"history,omitempty"`
}

func (m *Manifest) Ref() string { return m.Name + ":" + m.Tag }

var ErrNotFound = errors.New("image not found")

var (
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*$`)
	tagPattern  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	idPattern   = regexp.MustCompile(`^[0-9a-f]{3,64}$`)
)

// ParseRef splits "name[:tag]" and fills in the default tag.
func ParseRef(s string) (name, tag string, err error) {
	name, tag = s, "latest"
	if i := strings.LastIndex(s, ":"); i >= 0 && !strings.Contains(s[i:], "/") {
		name, tag = s[:i], s[i+1:]
	}
	if !namePattern.MatchString(name) || strings.Contains(name, "//") || strings.HasSuffix(name, "/") {
		return "", "", fmt.Errorf("invalid image name %q (use lowercase letters, digits, '.', '_', '-' and '/')", name)
	}
	if !tagPattern.MatchString(tag) {
		return "", "", fmt.Errorf("invalid tag %q", tag)
	}
	return name, tag, nil
}

func tagDir(name, tag string) string {
	return filepath.Join(config.Dir("images"), strings.ReplaceAll(name, "/", "%2F"), tag)
}

func TarPath(m *Manifest) string {
	return filepath.Join(tagDir(m.Name, m.Tag), "rootfs.tar")
}

func readManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("corrupt manifest in %s: %w", dir, err)
	}
	return &m, nil
}

// Load finds an image by name[:tag] or by id prefix.
func Load(ref string) (*Manifest, error) {
	if name, tag, err := ParseRef(ref); err == nil {
		m, err := readManifest(tagDir(name, tag))
		if err == nil {
			return m, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	id := strings.TrimPrefix(ref, "sha256:")
	if idPattern.MatchString(id) {
		all, _ := List()
		var hit *Manifest
		for i := range all {
			if strings.HasPrefix(all[i].ID, id) {
				if hit != nil && hit.ID != all[i].ID {
					return nil, fmt.Errorf("%q matches more than one image", ref)
				}
				if hit == nil {
					hit = &all[i]
				}
			}
		}
		if hit != nil {
			return hit, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, ref)
}

func List() ([]Manifest, error) {
	names, err := os.ReadDir(config.Dir("images"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Manifest
	for _, n := range names {
		tags, _ := os.ReadDir(filepath.Join(config.Dir("images"), n.Name()))
		for _, t := range tags {
			if m, err := readManifest(filepath.Join(config.Dir("images"), n.Name(), t.Name())); err == nil {
				out = append(out, *m)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

func Save(m *Manifest, tarFile string) error {
	d := tagDir(m.Name, m.Tag)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tarFile, filepath.Join(d, "rootfs.tar")); err != nil {
		return err
	}
	return writeManifest(d, m)
}

func writeManifest(dir string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644)
}

// Remove deletes one tag, or every tag of an image when given an id.
func Remove(ref string) ([]string, error) {
	m, err := Load(ref)
	if err != nil {
		return nil, err
	}
	victims := []*Manifest{m}
	if name, tag, err := ParseRef(ref); err != nil || name != m.Name || tag != m.Tag {
		victims = nil
		all, _ := List()
		for i := range all {
			if all[i].ID == m.ID {
				victims = append(victims, &all[i])
			}
		}
	}
	var gone []string
	for _, v := range victims {
		if err := os.RemoveAll(tagDir(v.Name, v.Tag)); err != nil {
			return gone, err
		}
		os.Remove(filepath.Dir(tagDir(v.Name, v.Tag)))
		gone = append(gone, v.Ref())
	}
	return gone, nil
}

// Tag gives an existing image another name.
func Tag(src, dst string) error {
	m, err := Load(src)
	if err != nil {
		return err
	}
	name, tag, err := ParseRef(dst)
	if err != nil {
		return err
	}
	from := TarPath(m)
	m.Name, m.Tag = name, tag
	d := tagDir(name, tag)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	to := filepath.Join(d, "rootfs.tar")
	os.Remove(to)
	if err := os.Link(from, to); err != nil {
		if err := CopyFile(from, to, 0o644); err != nil {
			return err
		}
	}
	return writeManifest(d, m)
}

func Extract(m *Manifest, dest string) error {
	f, err := os.Open(TarPath(m))
	if err != nil {
		return err
	}
	defer f.Close()
	return Unpack(f, dest)
}

// Commit packs a folder into a new image. m needs Name and Tag set.
func Commit(m *Manifest, rootfs string, skip map[string]bool) error {
	tmp := config.Dir("temp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(tmp, "image-*.tar")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())

	h := sha256.New()
	if err := PackSkip(rootfs, io.MultiWriter(f, h), skip); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	st, err := os.Stat(f.Name())
	if err != nil {
		return err
	}
	m.ID = hex.EncodeToString(h.Sum(nil))[:12]
	m.Size = st.Size()
	m.Created = time.Now()
	return Save(m, f.Name())
}

// Archive writes an image as a single tar (what `save` produces).
func Archive(ref string, w io.Writer) error {
	m, err := Load(ref)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	meta, _ := json.MarshalIndent(m, "", "  ")
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(meta))}); err != nil {
		return err
	}
	if _, err := tw.Write(meta); err != nil {
		return err
	}
	f, err := os.Open(TarPath(m))
	if err != nil {
		return err
	}
	defer f.Close()
	st, _ := f.Stat()
	if err := tw.WriteHeader(&tar.Header{Name: "rootfs.tar", Mode: 0o644, Size: st.Size()}); err != nil {
		return err
	}
	if _, err := io.Copy(tw, f); err != nil {
		return err
	}
	return tw.Close()
}

// Unarchive reads what Archive wrote and stores the image.
func Unarchive(r io.Reader) (*Manifest, error) {
	tr := tar.NewReader(r)
	h, err := tr.Next()
	if err != nil || h.Name != "manifest.json" {
		return nil, errors.New("not a lilDocker image archive")
	}
	var m Manifest
	if err := json.NewDecoder(tr).Decode(&m); err != nil {
		return nil, err
	}
	if _, _, err := ParseRef(m.Ref()); err != nil {
		return nil, err
	}
	if h, err = tr.Next(); err != nil || h.Name != "rootfs.tar" {
		return nil, errors.New("image archive is missing its filesystem")
	}
	tmp := config.Dir("temp")
	os.MkdirAll(tmp, 0o755)
	f, err := os.CreateTemp(tmp, "load-*.tar")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err := io.Copy(f, tr); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return &m, Save(&m, f.Name())
}

// Import turns a filesystem tarball (plain or gzipped) into an image.
func Import(r io.Reader, ref string) (*Manifest, error) {
	name, tag, err := ParseRef(ref)
	if err != nil {
		return nil, err
	}
	os.MkdirAll(config.Dir("temp"), 0o755)
	dir, err := os.MkdirTemp(config.Dir("temp"), "import-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := ApplyLayer(r, dir); err != nil {
		return nil, err
	}
	m := &Manifest{Name: name, Tag: tag, WorkDir: "/", History: []string{"import"}}
	return m, Commit(m, dir, nil)
}
