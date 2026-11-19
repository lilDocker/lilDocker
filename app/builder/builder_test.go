package builder

import (
	"os"
	"path/filepath"
	"testing"

	"lildocker/image"
)

func ctx(t *testing.T, dockerfile string, files map[string]string) string {
	t.Helper()
	t.Setenv("LILDOCKER_HOME", t.TempDir())
	dir := t.TempDir()
	files["lilDockfile"] = dockerfile
	for n, body := range files {
		p := filepath.Join(dir, n)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	return dir
}

func extract(t *testing.T, ref string) string {
	t.Helper()
	m, err := image.Load(ref)
	if err != nil {
		t.Fatal(err)
	}
	d := t.TempDir()
	if err := image.Extract(m, d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMetadataInstructions(t *testing.T) {
	dir := ctx(t, `ARG VERSION=1.0
ENV APP_HOME=/srv/$VERSION
LABEL maker=me "note=hello world"
WORKDIR $APP_HOME
USER nobody
VOLUME ["/data"]
EXPOSE 80 443/tcp
STOPSIGNAL SIGINT
ENTRYPOINT ["echo"]
CMD ["hi"]
`, map[string]string{"x": "1"})

	err := Build(Options{Context: dir, Tags: []string{"meta:v1", "meta:latest"}, Args: map[string]string{"VERSION": "2.5"}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := image.Load("meta:v1")
	if err != nil {
		t.Fatal(err)
	}
	if m.WorkDir != "/srv/2.5" || m.User != "nobody" || m.StopSignal != "SIGINT" {
		t.Errorf("metadata wrong: %+v", m)
	}
	if m.Labels["note"] != "hello world" || len(m.Expose) != 2 || m.Volumes[0] != "/data" {
		t.Errorf("labels/ports/volumes wrong: %+v", m)
	}
	if other, err := image.Load("meta"); err != nil || other.ID != m.ID {
		t.Errorf("second tag missing: %v", err)
	}
}

func TestMultiStage(t *testing.T) {
	dir := ctx(t, `FROM scratch AS build
WORKDIR /src
COPY input.txt .
RUN echo built > result.txt

FROM scratch
COPY --from=build /src/result.txt /final/result.txt
CMD echo done
`, map[string]string{"input.txt": "in"})

	if err := Build(Options{Context: dir, Tags: []string{"ms"}}); err != nil {
		t.Fatal(err)
	}
	out := extract(t, "ms")
	if _, err := os.Stat(filepath.Join(out, "final", "result.txt")); err != nil {
		t.Error("artifact was not copied from the build stage")
	}
	if _, err := os.Stat(filepath.Join(out, "src")); err == nil {
		t.Error("build stage files leaked into the final image")
	}

	if err := Build(Options{Context: dir, Tags: []string{"ms-build"}, Target: "build"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(extract(t, "ms-build"), "src", "input.txt")); err != nil {
		t.Error("--target should stop at the build stage")
	}
}

func TestFromLocalImageAndHistory(t *testing.T) {
	dir := ctx(t, "ENV A=1\nCOPY base.txt /base.txt\nCMD echo base\n", map[string]string{"base.txt": "b"})
	if err := Build(Options{Context: dir, Tags: []string{"base"}}); err != nil {
		t.Fatal(err)
	}
	child := t.TempDir()
	os.WriteFile(filepath.Join(child, "lilDockfile"), []byte("FROM base\nENV B=2\nCOPY more.txt /more.txt\n"), 0o644)
	os.WriteFile(filepath.Join(child, "more.txt"), []byte("m"), 0o644)
	if err := Build(Options{Context: child, Tags: []string{"child"}}); err != nil {
		t.Fatal(err)
	}
	m, _ := image.Load("child")
	if m.Cmd != "echo base" || len(m.Env) != 2 || len(m.History) < 5 {
		t.Errorf("inheritance wrong: %+v", m)
	}
	out := extract(t, "child")
	for _, f := range []string{"base.txt", "more.txt"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
}

func TestAddExtractsArchiveAndIgnore(t *testing.T) {
	dir := ctx(t, "COPY . /app\nCMD echo\n", map[string]string{"keep.txt": "k", "skip.log": "s", "sub/a.txt": "a", ".lildockerignore": "*.log\n"})
	if err := Build(Options{Context: dir, Tags: []string{"ign"}}); err != nil {
		t.Fatal(err)
	}
	out := extract(t, "ign")
	if _, err := os.Stat(filepath.Join(out, "app", "skip.log")); err == nil {
		t.Error(".lildockerignore was not honoured")
	}
	if _, err := os.Stat(filepath.Join(out, "app", "sub", "a.txt")); err != nil {
		t.Error("nested file missing")
	}
}

func TestErrors(t *testing.T) {
	dir := ctx(t, "COPY ../secret /x\n", map[string]string{})
	if err := Build(Options{Context: dir, Tags: []string{"bad"}}); err == nil {
		t.Error("COPY outside the context must fail")
	}
	dir = ctx(t, "FROM scratch\nCOPY --from=nope a b\n", map[string]string{})
	if err := Build(Options{Context: dir, Tags: []string{"bad"}}); err == nil {
		t.Error("unknown stage must fail")
	}
	dir = ctx(t, "FROM scratch\nRUN exit 3\n", map[string]string{})
	if err := Build(Options{Context: dir, Tags: []string{"bad"}}); err == nil {
		t.Error("failing RUN must fail the build")
	}
}
