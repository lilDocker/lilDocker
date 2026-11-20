<p align="center">
  <img alt="lilDocker logo" src="assets/lilDocker.png" width="720">
</p>

<p align="center">
  <b>lilDocker</b> a small Docker style container tool, written in Go
</p>

<p align="center">
  build images from a lilDockfile, run them as containers, no daemon needed
</p>


> **New to containers?** Don't worry, this guide goes step by step and skips nothing.


## Table of Contents

1. [What is lilDocker?](#what-is-lildocker)
2. [Before You Start](#before-you-start)
3. [Installation](#installation)
4. [Your First Container](#your-first-container)
5. [Commands](#commands)
6. [Run Options](#run-options)
7. [Writing a lilDockfile](#writing-a-lildockfile)
8. [Volumes and Ports](#volumes-and-ports)
9. [Images: Tags, Save, Load, Pull](#images-tags-save-load-pull)
10. [Supported Languages](#supported-languages)
11. [Isolation](#isolation)
12. [Compared With Docker](#compared-with-docker)
13. [Where Things Are Stored](#where-things-are-stored)
14. [Environment Variables](#environment-variables)
15. [Troubleshooting](#troubleshooting)

<br/>

## What is lilDocker?

**lilDocker** is a container tool that runs in your terminal and has the same feel as Docker. You describe your app in a `lilDockfile`, build it into an image, and start containers from that image.

It is not real Docker. By default a container is a private folder plus a process started inside it, with no kernel walls around it. That keeps it small enough to read in an afternoon, and it runs on **Linux, macOS and Windows**. On Linux you can add `--isolate` to get a chroot and separate namespaces.

**lilDocker can:**
- Build images from a lilDockfile, including multi-stage builds, build args and tags
- Run containers in the foreground or background, with volumes, published ports and restart policies
- Start, stop, restart, pause, kill, rename, copy files in and out, diff and commit containers
- Show logs (and follow them), run commands inside a container, inspect anything as JSON
- Tag, save, load, import, export and pull images
- Manage named volumes and clean up with `prune`
- Run **any language** whose toolchain is installed on your machine

<br/>

## Before You Start

You need:
- **Go 1.24** or newer, to build lilDocker itself
- The toolchain of whatever language you want to run (Python, Node, g++ and so on)

<br/>

## Installation

```bash
# 1. Go to the app folder
cd app

# 2. Build the binary
go build -o lildocker ./cmd

# 3. Check it works
./lildocker version
```

On Windows the output is `lildocker.exe` and you run it as `.\lildocker.exe`. To use it from anywhere, put the binary in a folder on your `PATH`.

<br/>

## Your First Container

The repo ships with a small Node sample.

```bash
# build an image called demo from the sample folder
./lildocker build -t demo sample-node

# run it
./lildocker run demo

# run it in the background, with a name
./lildocker run -d --name web demo

# see what is running, then read its output
./lildocker ps
./lildocker logs web

# stop it and clean up
./lildocker stop web
./lildocker rm web
```

The older short form `build <path> <image>` still works too.

<br/>

## Commands

Run `lildocker <command> --help` for the options of any command.

### Containers

| Command | What it does |
|---------|--------------|
| `run [options] <image> [cmd...]` | Create and start a container |
| `create [options] <image> [cmd...]` | Create a container without starting it |
| `start [-a] <container>...` | Start stopped containers |
| `stop [-t secs] <container>...` | Stop containers (SIGTERM, then SIGKILL after the timeout) |
| `restart [-t secs] <container>...` | Stop and start again |
| `kill [-s signal] <container>...` | Send a signal, SIGKILL by default |
| `pause` / `unpause <container>...` | Freeze and resume processes (not on Windows) |
| `rm [-f] <container>...` | Remove containers (`-f` stops them first) |
| `ps [-a] [-q] [-n N] [-f filter] [--format T]` | List containers |
| `logs [-f] [--tail N] <container>` | Print output, optionally following it |
| `attach <container>` | Follow a container's output |
| `exec [-e K=V] [-w dir] <container> <cmd>...` | Run a command inside a container's folder |
| `inspect [-f T] <name>...` | Show a container, image or volume as JSON |
| `top <container>` | Show the container's process |
| `port <container>` | List published ports |
| `rename <container> <new>` | Rename a container |
| `wait <container>...` | Block until containers stop, print exit codes |
| `cp <container>:<path> <dest>` | Copy out of a container (or `cp <src> <container>:<path>` to copy in) |
| `diff <container>` | Show added (A), changed (C) and deleted (D) files |
| `commit <container> <image[:tag]>` | Save the container's filesystem as an image |
| `export [-o file] <container>` | Write the container's filesystem as a tar |
| `update --restart <policy> <container>...` | Change the restart policy |
| `prune [-f]` | Remove all stopped containers |

### Images

| Command | What it does |
|---------|--------------|
| `build [-t name[:tag]] [-f file] [--build-arg K=V] [--target stage] <path>` | Build an image |
| `images [-q] [--format T] [name]` | List images |
| `rmi [-f] <image>...` | Remove images, by name:tag or id |
| `tag <source> <target[:tag]>` | Give an image another name |
| `history <image>` | Show the instructions an image was built from |
| `save [-o file] <image>` / `load [-i file]` | Move an image to or from a tar file |
| `import <file\|-> <image[:tag]>` | Make an image from a filesystem tarball |
| `pull <image[:tag]>` | Download an image from a registry |

### Volumes and system

| Command | What it does |
|---------|--------------|
| `volume create [name]` / `ls` / `rm` / `inspect` / `prune` | Manage named volumes |
| `system df` | Disk usage of images, containers and volumes |
| `system prune [-a] [--volumes] [-f]` | Remove stopped containers, and optionally unused images and volumes |
| `info` | Counts, storage path and platform |
| `logo [--color]` | Show the logo |
| `version` | Version information |

### Docker-style groups

The longer names work as well: `container ls`, `container rm`, `image ls`, `image rm`, `image prune [-a]`, `volume ls`, `system df` and so on.

### Names, ids and templates

- A container can be named by its name or by the first letters of its id. An image by `name`, `name:tag` or id.
- `ps` and `images` accept `--format` with a Go template, for example `--format '{{.Names}} {{.Status}}'`. `inspect -f` uses the field names shown in `inspect`'s output, such as `{{.Pid}}` or `{{.Status}}`.
- `ps` fields: `ID Image Command RunningFor Status State Ports Names`. `images` fields: `Repository Tag ID CreatedSince Size`.
- `ps -f` filters: `name=`, `id=`, `status=`, `ancestor=`.
- Combined short flags work: `run -dit`, `rm -fv`.

<br/>

## Run Options

| Option | Description |
|--------|-------------|
| `-d`, `--detach` | Run in the background and print the container id |
| `--name <name>` | Give the container a name (a random one is chosen otherwise) |
| `-e`, `--env KEY=VALUE` | Set an environment variable, repeat for more. `-e KEY` copies it from your shell |
| `--env-file <file>` | Read `KEY=VALUE` lines from a file |
| `-w`, `--workdir <dir>` | Working directory inside the container |
| `-v`, `--volume [src:]target[:ro]` | Mount a folder or named volume, repeatable |
| `-p`, `--publish [ip:]host:container` | Publish a TCP port, repeatable |
| `--restart <policy>` | `no`, `always`, `unless-stopped`, `on-failure[:N]` |
| `--entrypoint <cmd>` | Replace the image's entrypoint |
| `-h`, `--hostname <name>` | Sets `$HOSTNAME` |
| `--rm` | Remove the container when it exits |
| `--isolate` | chroot and namespaces (Linux, needs root) |
| `-i`, `-t` | Accepted for Docker habits and ignored |

Anything after the image name replaces the image's `CMD`:

```bash
./lildocker run demo node -v
```

<br/>

## Writing a lilDockfile

```
# FROM takes scratch, or the name of an image you built or pulled earlier
FROM scratch
ARG VERSION=1.0
ENV APP_HOME=/srv/app-$VERSION
LABEL maintainer=me
WORKDIR $APP_HOME
COPY app.js .
RUN echo building
EXPOSE 3000
VOLUME /data
USER nobody
STOPSIGNAL SIGINT
ENTRYPOINT ["node"]
CMD ["app.js"]
```

| Instruction | Meaning |
|-------------|---------|
| `FROM` | Start a stage from `scratch` or an image. `FROM image AS name` names it for multi-stage builds |
| `ARG` | A build variable, set with `--build-arg`. Also visible to `RUN` |
| `ENV` | Set an environment variable, kept in the image |
| `LABEL` | Attach key=value metadata |
| `WORKDIR` | Set the working directory, created if missing |
| `COPY` | Copy files, folders or globs from the build folder. `COPY --from=<stage>` copies from an earlier stage |
| `ADD` | Like `COPY`, and unpacks local `.tar`, `.tar.gz` and `.tgz` files |
| `RUN` | Run a command while building, in the image's folder |
| `EXPOSE` | Note a port. Metadata only |
| `VOLUME` | Folders that get their own volume when the container starts |
| `USER` | Recorded only, lilDocker does not switch users |
| `STOPSIGNAL` | Signal used by `stop` |
| `CMD` / `ENTRYPOINT` | Default command. A shell line or a JSON array. `run` arguments replace `CMD` |

Rules:
- `$VAR` and `${VAR}` are expanded in every instruction except `RUN`, `CMD` and `ENTRYPOINT`, where the shell does it.
- `COPY` paths that leave the build folder are rejected. A `.lildockerignore` next to the lilDockfile leaves files out.
- A lilDockfile with no `FROM` and no `COPY` packs the whole folder.
- Comments start with `#` on their own line. A trailing `\` continues a line.
- An image does not need a `CMD`, but then `run` needs a command.

### Multi-stage builds

Compile in one stage, ship only the result:

```
FROM scratch AS build
WORKDIR /src
COPY . .
ENV CGO_ENABLED=0
RUN go build -o app .

FROM scratch
COPY --from=build /src/app /app
ENTRYPOINT ["/app"]
```

That is `examples/go-multistage`. `build --target build` stops after the first stage.

<br/>

## Volumes and Ports

```bash
# a folder from your machine
./lildocker run -v ./notes:/data demo

# a named volume, created on first use and kept after the container is gone
./lildocker run -v mydata:/data demo
./lildocker volume ls

# read-only (enforced with --isolate)
sudo ./lildocker run --rm --isolate -v ./config:/etc/app:ro hello-rust

# publish a port: your 8080 reaches the program listening on 3000
./lildocker run -d -p 8080:3000 demo
```

How they work:
- Without `--isolate` a volume is a symlink inside the container folder, so relative paths and container paths both reach it. `:ro` is only enforced with `--isolate`, which uses real bind mounts.
- Containers share your machine's network. `-p host:container` starts a small relay from the host port to the container's port. If both numbers are the same, nothing is needed since the program is already reachable.
- Removing a container never deletes the contents of a mounted folder, and `rm` refuses to run if anything is still mounted under the container.

### Restart policies

```bash
./lildocker run -d --restart on-failure:3 demo
./lildocker run -d --restart unless-stopped demo
./lildocker update --restart no web
```

`stop` always wins: a stopped container is not restarted.

<br/>

## Images: Tags, Save, Load, Pull

```bash
./lildocker build -t app:1.0 -t app:latest .
./lildocker tag app:1.0 team/app:stable
./lildocker save -o app.tar app:1.0        # move it to another machine
./lildocker load -i app.tar
./lildocker export -o fs.tar web           # a container's files
./lildocker import fs.tar app:restored
./lildocker commit web app:snapshot
```

### Pulling

```bash
./lildocker pull alpine
sudo ./lildocker run --rm alpine echo hello
```

`pull` speaks the registry v2 protocol (Docker Hub by default, or `host/name`). `run` and `FROM` pull automatically when an image is missing. Things to know:

- Pulled images are Linux root filesystems. They run **only on Linux, as root**, and always under `--isolate`, because their programs must come from inside the image. `RUN` steps on top of a pulled base run inside it too.
- Public images only. There is no login and no `push`. Use `save` and `load` to move images.
- Layers compressed with zstd are not supported.
- This was tested against a simulated registry (token login, multi-platform index, gzip layers, whiteouts, checksums). It has **not** been tried against Docker Hub itself.

<br/>

## Supported Languages

lilDocker does not bundle any language. `RUN` and `CMD` use the tools installed on your machine, so **if a language has a command-line toolchain, it works**. Compiled languages are built during `lildocker build`, and the finished program is what gets packed into the image.

Every folder in `examples/` is a ready-to-run lilDockfile:

```bash
./lildocker build -t hello-cpp ../examples/cpp
./lildocker run --rm hello-cpp
```

### Tested

| Language | Example folder | You need | Build step | Run command |
|----------|----------------|----------|------------|-------------|
| Python | `python` | python3 | none | `CMD python3 main.py` |
| JavaScript (Node.js) | `node` | Node.js | none | `CMD node main.js` |
| Ruby | `ruby` | ruby | none | `CMD ruby main.rb` |
| PHP | `php` | php | none | `CMD php main.php` |
| Perl | `perl` | perl | none | `CMD perl main.pl` |
| Shell (sh / bash) | `shell` | sh | none | `CMD sh main.sh` |
| C | `c` | gcc | `RUN gcc main.c -o hi` | `CMD ./hi` |
| C++ | `cpp` | g++ | `RUN g++ -std=c++17 main.cpp -o hi` | `CMD ./hi` |
| Go | `go` | Go | `RUN go build -o hi .` | `CMD ./hi` |
| Rust | `rust` | rustc | `RUN rustc main.rs -o hi` | `CMD ./hi` |
| Rust (cargo project) | `rust-cargo` | cargo | `RUN cargo build --release` | `CMD ./target/release/hi` |
| Java | `java` | JDK | `RUN javac Main.java` | `CMD java Main` |

### Written but not tested yet

| Language | Example folder | You need | Build step | Run command |
|----------|----------------|----------|------------|-------------|
| C# | `csharp` | .NET SDK 8 | `RUN dotnet build -c Release -o out` | `CMD dotnet out/app.dll` |

### Should work the same way

TypeScript (via Bun or Deno), Kotlin, Swift, Dart, Lua, R, Elixir, Haskell, Zig and anything else with a command-line toolchain. Not tried.

### Self-contained images

The `*-static` examples (`c-static`, `cpp-static`, `rust-static`, `go-static`) and `go-multistage` build a fully static binary. Those images need nothing from the host, so they run under [`--isolate`](#isolation):

```bash
./lildocker build -t hello-rust ../examples/rust-static
sudo ./lildocker run --rm --isolate hello-rust
```

Interpreted languages and Java can only be self-contained by pulling a base image that carries the runtime (see [Pulling](#pulling)).

<br/>

## Isolation

By default a container is a folder and a process, with no walls. Your machine's programs and `PATH` are used, though an absolute command path like `/app/hello` is looked up inside the container first.

With `--isolate` on Linux (as root), lilDocker:
- chroots the process into the container folder
- gives it its own process, mount, hostname and IPC namespaces
- provides `/dev/null`, `/dev/zero`, `/dev/random`, `/dev/urandom`, `/dev/tty` and a `resolv.conf`
- starts it with only the image's environment
- uses real bind mounts for `-v`, and enforces `:ro`

The image must then carry everything it needs, including a shell if you use a shell-form `CMD`.

Things to know:
- `RUN` on an image built from `scratch` runs on your machine, in the image's folder. It is **not** sandboxed, so an absolute path like `/etc` means your real `/etc`.
- There is no network isolation, no memory or CPU limits and no user switching.

<br/>

## Compared With Docker

| Docker feature | lilDocker |
|----------------|-----------|
| `run`, `create`, `start`, `stop`, `restart`, `kill`, `pause`, `rm`, `ps`, `logs`, `exec`, `inspect`, `cp`, `diff`, `commit`, `export`, `rename`, `wait`, `top`, `port`, `update` | Supported |
| `build`, `images`, `rmi`, `tag`, `history`, `save`, `load`, `import`, `pull` | Supported |
| `volume`, `system df`, `system prune`, `info`, `version`, `prune` | Supported |
| `-v`, `-p`, `-e`, `--env-file`, `--restart`, `--entrypoint`, `--rm`, `-d`, `--name` | Supported |
| Multi-stage builds, `ARG`, `LABEL`, `ADD`, `VOLUME`, `STOPSIGNAL`, `.dockerignore` style ignore file | Supported (`.lildockerignore`) |
| `--format` templates, `-f` filters | Supported on `ps`, `images`, `inspect` |
| `HEALTHCHECK`, `ONBUILD`, `SHELL` | Not supported, the build stops with an error |
| `COPY --chown`, `--chmod` | Accepted and ignored |
| `login`, `push`, `search`, private registries | Not supported |
| `network`, `compose`, `swarm`, `stats`, `events`, `plugin`, `context`, `buildx` | Not supported, the command explains why |
| Memory and CPU limits, `--user`, UDP ports, `-P` | Not supported |
| Real layers and layer caching | No, an image is one tar |
| Real interactive terminals (`-it`) | The flags are accepted and ignored |

<br/>

## Where Things Are Stored

Everything goes under `~/.lildocker`, or `$LILDOCKER_HOME` if you set it:

```
images/<name>/<tag>/manifest.json
images/<name>/<tag>/rootfs.tar
containers/<id>/state.json
containers/<id>/container.log
containers/<id>/rootfs/
volumes/<name>/
```

<br/>

## Environment Variables

| Variable | Description |
|----------|-------------|
| `LILDOCKER_HOME` | Where images, containers and volumes are stored (default `~/.lildocker`) |
| `LILDOCKER_NO_LOGO` | Set to anything to hide the logo shown when you run `lildocker` with no arguments |
| `NO_COLOR` | Draw the logo without colour |

The logo is the same ASCII picture every time, built into the program, so it works from any folder. It only appears in a real terminal, never in pipes or scripts, and it shrinks to fit narrow windows.

<br/>

Run the tests from the `app` folder:

```bash
go test ./...
```

<br/>

## Troubleshooting

| Problem | Solution |
|---------|----------|
| `fork/exec ...: no such file or directory` | The command isn't installed on your machine, or isn't on your `PATH`. Install the language toolchain |
| `image has no command, pass one to run` | The image has no `CMD` or `ENTRYPOINT`. Add one, or run `lildocker run <image> <command>` |
| `unknown instruction` | Check the spelling against the instruction table |
| `image not found: x (pulling it failed ...)` | Nothing local matches. Run `./lildocker images`, or check your network if you meant to pull |
| `the name "x" is already in use` | Pick another `--name`, or `./lildocker rm x` |
| `x is running, stop it first or use -f` | `./lildocker stop x`, or `rm -f x` |
| `x is used by ...` when removing an image | Remove the containers that use it first, or use `rmi -f` |
| `isolation needs linux and root` | `--isolate` only works on Linux. Run with `sudo` |
| `not found inside the container` under `--isolate` | The program isn't in the image. Use a static binary, or pull a base image |
| `is a linux image and can only run on linux` | Pulled images are Linux root filesystems. Use Linux, or build from `scratch` |
| `publishing 8080:80: address already in use` | Pick another host port |
| `linking /data: ... developer mode` (Windows) | Volumes use symlinks. Turn on Developer Mode or use an admin shell |
| A background container exits right away | Run `./lildocker logs <name>` to see why |
| `still has mounted folders, refusing to delete it` | A crashed `--isolate` run left a mount. Unmount it, or reboot, then `rm` again |
| Windows: `RUN` fails on quotes | `RUN` goes through `cmd /C` there. Keep commands simple or use a script file |
