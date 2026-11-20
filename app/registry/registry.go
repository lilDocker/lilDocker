package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"

	"lildocker/config"
	"lildocker/image"
)

const hub = "registry-1.docker.io"

const (
	mtIndexOCI    = "application/vnd.oci.image.index.v1+json"
	mtIndexDocker = "application/vnd.docker.distribution.manifest.list.v2+json"
	mtOCI         = "application/vnd.oci.image.manifest.v1+json"
	mtDocker      = "application/vnd.docker.distribution.manifest.v2+json"
)

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  *struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	} `json:"platform"`
}

type manifestDoc struct {
	MediaType string       `json:"mediaType"`
	Manifests []descriptor `json:"manifests"`
	Config    descriptor   `json:"config"`
	Layers    []descriptor `json:"layers"`
}

type imageConfig struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Config       struct {
		Env          []string            `json:"Env"`
		Cmd          []string            `json:"Cmd"`
		Entrypoint   []string            `json:"Entrypoint"`
		WorkingDir   string              `json:"WorkingDir"`
		User         string              `json:"User"`
		StopSignal   string              `json:"StopSignal"`
		Labels       map[string]string   `json:"Labels"`
		ExposedPorts map[string]struct{} `json:"ExposedPorts"`
		Volumes      map[string]struct{} `json:"Volumes"`
	} `json:"config"`
}

type reference struct {
	host, repo, ref string
	name, tag       string
}

var hostLike = regexp.MustCompile(`^(localhost|[^/]*[.:][^/]*)$`)

func parse(s string) (reference, error) {
	var r reference
	rest := s
	if i := strings.Index(rest, "/"); i > 0 && hostLike.MatchString(rest[:i]) {
		r.host, rest = rest[:i], rest[i+1:]
	} else {
		r.host = hub
	}
	r.tag = "latest"
	r.ref = ""
	if i := strings.Index(rest, "@"); i >= 0 {
		r.ref, rest = rest[i+1:], rest[:i]
	} else if i := strings.LastIndex(rest, ":"); i >= 0 && !strings.Contains(rest[i:], "/") {
		r.tag, rest = rest[i+1:], rest[:i]
	}
	if r.ref == "" {
		r.ref = r.tag
	}
	r.repo = rest
	if r.host == hub && !strings.Contains(rest, "/") {
		r.repo = "library/" + rest
	}
	r.name = strings.TrimPrefix(r.repo, "library/")
	if r.host != hub {
		r.name = strings.ReplaceAll(r.host, ":", "_") + "/" + r.repo
	}
	if _, _, err := image.ParseRef(r.name + ":" + r.tag); err != nil {
		return r, err
	}
	return r, nil
}

type client struct {
	http  *http.Client
	base  string
	repo  string
	token string
}

func (c *client) get(path string, accept ...string) (*http.Response, error) {
	for try := 0; ; try++ {
		req, err := http.NewRequest("GET", c.base+path, nil)
		if err != nil {
			return nil, err
		}
		if len(accept) > 0 {
			req.Header.Set("Accept", strings.Join(accept, ", "))
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && try == 0 {
			challenge := resp.Header.Get("Www-Authenticate")
			resp.Body.Close()
			if err := c.authenticate(challenge); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				return nil, fmt.Errorf("not found in registry: %s", path)
			}
			return nil, fmt.Errorf("registry returned %s for %s", resp.Status, path)
		}
		return resp, nil
	}
}

var param = regexp.MustCompile(`(\w+)="([^"]*)"`)

func (c *client) authenticate(challenge string) error {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer") {
		return errors.New("registry needs credentials, which lilDocker does not support")
	}
	vals := map[string]string{}
	for _, m := range param.FindAllStringSubmatch(challenge, -1) {
		vals[m[1]] = m[2]
	}
	if vals["realm"] == "" {
		return errors.New("registry sent an unusable auth challenge")
	}
	q := url.Values{}
	if vals["service"] != "" {
		q.Set("service", vals["service"])
	}
	q.Set("scope", "repository:"+c.repo+":pull")
	resp, err := c.http.Get(vals["realm"] + "?" + q.Encode())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token request failed: %s", resp.Status)
	}
	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return err
	}
	c.token = t.Token
	if c.token == "" {
		c.token = t.AccessToken
	}
	return nil
}

func (c *client) json(path string, v any, accept ...string) error {
	resp, err := c.get(path, accept...)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// Pull downloads an image from a registry and stores it locally.
func Pull(ref string, out io.Writer) (*image.Manifest, error) {
	r, err := parse(ref)
	if err != nil {
		return nil, err
	}
	scheme := "https"
	if strings.HasPrefix(r.host, "localhost") || strings.HasPrefix(r.host, "127.0.0.1") {
		scheme = "http"
	}
	c := &client{http: &http.Client{Timeout: 0}, base: scheme + "://" + r.host, repo: r.repo}

	fmt.Fprintf(out, "Pulling %s from %s\n", r.name+":"+r.tag, r.host)
	accept := []string{mtOCI, mtDocker, mtIndexOCI, mtIndexDocker}
	var doc manifestDoc
	if err := c.json("/v2/"+r.repo+"/manifests/"+r.ref, &doc, accept...); err != nil {
		return nil, err
	}
	if len(doc.Manifests) > 0 {
		var pick *descriptor
		for i, d := range doc.Manifests {
			if d.Platform != nil && d.Platform.OS == "linux" && d.Platform.Architecture == runtime.GOARCH {
				pick = &doc.Manifests[i]
				break
			}
		}
		if pick == nil {
			return nil, fmt.Errorf("no linux/%s build of %s", runtime.GOARCH, ref)
		}
		doc = manifestDoc{}
		if err := c.json("/v2/"+r.repo+"/manifests/"+pick.Digest, &doc, accept...); err != nil {
			return nil, err
		}
	}
	if len(doc.Layers) == 0 {
		return nil, errors.New("registry returned a manifest with no layers")
	}

	var cfg imageConfig
	if err := c.json("/v2/"+r.repo+"/blobs/"+doc.Config.Digest, &cfg); err != nil {
		return nil, err
	}

	os.MkdirAll(config.Dir("temp"), 0o755)
	stage, err := os.MkdirTemp(config.Dir("temp"), "pull-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)

	for i, l := range doc.Layers {
		if strings.Contains(l.MediaType, "zstd") {
			return nil, errors.New("this image uses zstd layers, which lilDocker cannot read")
		}
		fmt.Fprintf(out, "  layer %d/%d  %s  %s\n", i+1, len(doc.Layers), short(l.Digest), human(l.Size))
		if err := c.layer(r.repo, l, stage); err != nil {
			return nil, err
		}
	}

	m := &image.Manifest{
		Name:       r.name,
		Tag:        r.tag,
		OS:         "linux",
		Arch:       cfg.Architecture,
		Base:       ref,
		Env:        cfg.Config.Env,
		WorkDir:    cfg.Config.WorkingDir,
		Entrypoint: arrayString(cfg.Config.Entrypoint),
		Cmd:        arrayString(cfg.Config.Cmd),
		User:       cfg.Config.User,
		StopSignal: cfg.Config.StopSignal,
		Labels:     cfg.Config.Labels,
		History:    []string{"pull " + ref},
	}
	if m.WorkDir == "" {
		m.WorkDir = "/"
	}
	for p := range cfg.Config.ExposedPorts {
		m.Expose = append(m.Expose, p)
	}
	for v := range cfg.Config.Volumes {
		m.Volumes = append(m.Volumes, v)
	}
	if err := image.Commit(m, stage, nil); err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "Pulled %s (%s)\n", m.Ref(), m.ID)
	return m, nil
}

func (c *client) layer(repo string, l descriptor, dest string) error {
	resp, err := c.get("/v2/" + repo + "/blobs/" + l.Digest)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	h := sha256.New()
	body := io.TeeReader(resp.Body, h)
	if err := image.ApplyLayer(body, dest); err != nil {
		return err
	}
	io.Copy(io.Discard, body)
	if want := strings.TrimPrefix(l.Digest, "sha256:"); hex.EncodeToString(h.Sum(nil)) != want {
		return fmt.Errorf("layer %s failed its checksum", short(l.Digest))
	}
	return nil
}

func arrayString(a []string) string {
	if len(a) == 0 {
		return ""
	}
	b, _ := json.Marshal(a)
	return string(b)
}

func short(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		d = d[:12]
	}
	return d
}

func human(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%d B", n)
	case n < k*k:
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
}
