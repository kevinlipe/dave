# Dave — Module and Dependency Setup

This document covers the Go module setup, exact dependencies, and CGo build configuration for Dave. Get this working before writing any editor code.

---

## Module Name

```
module dave

go 1.22
```

The module name is simply `dave`. It is a personal tool, not a published package, so there is no need for a full domain-qualified module path. If you later want to publish it, rename the module then.

---

## Dependencies

Dave has exactly three external Go dependencies:

| Package | Purpose |
|---|---|
| `github.com/veandco/go-sdl2/sdl` | SDL2 bindings — window, rendering, input |
| `github.com/veandco/go-sdl2/ttf` | SDL_ttf bindings — TTF font rendering |
| `golang.org/x/image/font/gofont/gomono` | Go Mono font data for embedding |

Add them:

```
go get github.com/veandco/go-sdl2/sdl@latest
go get github.com/veandco/go-sdl2/ttf@latest
go get golang.org/x/image@latest
```

Pin the exact versions in `go.sum` immediately after — do not use floating `latest` references in the final `go.mod`. Run `go mod tidy` to clean up.

**That's it.** There is no Lua, no scripting runtime, no other CGo dependency. The only C code Dave touches is SDL2 and SDL_ttf, via go-sdl2's existing CGo bindings. Dave's own Go code is pure Go throughout.

---

## System Dependencies (not Go packages)

These must be installed on the build machine. They are not managed by Go modules.

**Linux:**
```
# Debian/Ubuntu
sudo apt install libsdl2-dev libsdl2-ttf-dev

# openSUSE Tumbleweed
sudo zypper install libSDL2-devel SDL2_ttf-devel
```

**Windows:**
- Download SDL2 development libraries (MinGW) from https://github.com/libsdl-org/SDL/releases
- Download SDL2_ttf development libraries (MinGW) from https://github.com/libsdl-org/SDL_ttf/releases
- Extract to a known location (e.g. `C:\SDL2\`)
- Set CGo flags (see Windows CGo section below)

SDL2 and SDL_ttf do **not** need to be statically linked for development. For distribution, see the Distribution section below.

---

## CGo Configuration

go-sdl2 handles its own CGo setup internally — you do not need to write any CGo yourself. Dave's own packages are pure Go. The only thing you need to ensure is that the C compiler can find the SDL2 headers and libraries.

**Linux:** If SDL2 dev packages are installed via the package manager, CGo will find them automatically via pkg-config. No additional configuration needed.

**Windows:** Set these environment variables before building, or add them to your shell profile:

```powershell
$env:CGO_CFLAGS="-IC:/SDL2/include"
$env:CGO_LDFLAGS="-LC:/SDL2/lib -lSDL2 -LC:/SDL2_ttf/lib -lSDL2_ttf"
```

Adjust paths to match where you extracted the SDL2 development libraries.

Alternatively, go-sdl2 supports a `WINDOWS_SDL2_PATH` environment variable — check the go-sdl2 README for the current recommended approach, as this may change between versions.

---

## Embedded Go Mono Font

Go Mono is embedded in the binary using Go's `//go:embed` directive. This makes it available on any machine regardless of what fonts are installed.

The font data lives in `golang.org/x/image/font/gofont/gomono`. Create `render/fonts.go`:

```go
package render

import (
    _ "embed"
    "golang.org/x/image/font/gofont/gomono"
)

// GoMonoTTF is the embedded Go Mono font data.
// This is used as the default font when no font is specified in config.
var GoMonoTTF []byte = gomono.TTF
```

`gomono.TTF` is a `[]byte` of the raw TTF file. Pass it to SDL_ttf via `TTF_OpenFontRW` (go-sdl2 exposes this as `ttf.OpenFontRW`) rather than `TTF_OpenFont`, since you have the data in memory rather than a file path.

---

## Build Tags

Dave uses one build tag: `sdl` is implicit via the go-sdl2 import. No custom build tags are needed.

The `sdave` binary shares all non-rendering packages with Dave and has no SDL dependency at all. Its build is plain `go build ./sdave` with no special flags.

---

## Verifying the Build

Create a minimal `main.go` before writing any editor code:

```go
package main

import (
    "fmt"
    "github.com/veandco/go-sdl2/sdl"
    "github.com/veandco/go-sdl2/ttf"
)

func main() {
    if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
        panic(err)
    }
    defer sdl.Quit()

    if err := ttf.Init(); err != nil {
        panic(err)
    }
    defer ttf.Quit()

    fmt.Println("SDL2 and SDL_ttf initialized successfully.")
}
```

`go build .` and run it. If it prints the message and exits cleanly, the build is working. Fix any CGo or linker errors before writing any editor code.

---

## Project Layout

```
dave/
  go.mod
  go.sum
  main.go
  sdave/
    main.go
  buffer/
  file/
  addr/
  cmd/
  regexp/
  pane/
  render/
    fonts.go      ← embedded Go Mono lives here
    colors.go     ← color constants
  input/
  menu/
  config/
```

---

## Distribution

**Linux:** go-sdl2 supports fully static builds via the `-tags static` build flag, which uses bundled SDL2 libraries included in go-sdl2 itself:

```
CGO_ENABLED=1 go build -tags static -ldflags "-s -w" .
```

This produces a single self-contained binary with no external SDL2 dependency. This is the recommended approach for Linux distribution.

**Windows:** Build natively on a personal Windows machine with Go and MinGW installed. With the SDL2 and SDL2_ttf MinGW dev packages present (required for development anyway), a static exe is simply:

```bash
go build -tags static -ldflags "-s -w -H windowsgui" -o dave.exe .
```

`-H windowsgui` suppresses the console window. `-tags static` links SDL2 and SDL2_ttf into the binary via go-sdl2's bundled static libraries.

The result is a single `dave.exe` — no DLLs. Copy it to any Windows machine and run it.

---

## go.mod Reference

After setup, your `go.mod` should look approximately like this (exact versions will differ):

```
module dave

go 1.22

require (
    github.com/veandco/go-sdl2 v0.4.38
    golang.org/x/image v0.18.0
)
```

Run `go mod tidy` after any dependency change to keep `go.sum` consistent.
