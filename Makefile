# Dave build rules.
#
# Targets:
#   all     - build dave / dave.exe (dynamically linked) and sdave
#   sdave   - build sdave only
#   static  - build self-contained exe with SDL2 statically linked (Windows only)
#   test    - run the full Go test suite
#   check   - build + test (use before pushing / from CI)
#   install - install dave and sdave via go install
#   clean   - remove build artifacts
#
# On Windows the 'static' target needs the MSYS2 ucrt64 static SDL2 library.
# SDL2_LIBDIR is auto-detected from common install locations; override if needed:
#
#   make static SDL2_LIBDIR=/c/msys64/ucrt64/lib

ifeq ($(OS),Windows_NT)
    BINARY = dave.exe
else
    BINARY = dave
endif

# Auto-detect the MSYS2 ucrt64 lib dir by looking for libSDL2.a in common locations. 
# (I know this is probably a bad idea, but it's what we're doing.)
SDL2_LIBDIR ?= $(patsubst %/libSDL2.a,%,$(firstword $(wildcard \
	/ucrt64/lib/libSDL2.a \
	/c/msys64/ucrt64/lib/libSDL2.a \
	/c/tools/msys64/ucrt64/lib/libSDL2.a \
	$(HOME)/scoop/apps/msys2/current/ucrt64/lib/libSDL2.a)))

SDL2_STATIC_LDFLAGS = \
	-L$(SDL2_LIBDIR) \
	-lSDL2 -static \
	-lm -ldinput8 -ldxguid -ldxerr8 \
	-luser32 -lgdi32 -lwinmm -limm32 \
	-lole32 -loleaut32 -lshell32 \
	-lversion -luuid -lsetupapi \
	-static-libgcc

.PHONY: all sdave static test check clean install

all:
	go build -v -o $(BINARY) .
	go build -v -o sdave/sdave ./sdave

sdave:
	go build -v -o sdave/sdave ./sdave

static:
	CGO_LDFLAGS="$(SDL2_STATIC_LDFLAGS)" go build -v -o $(BINARY) .

test:
	go test ./...

check: all test

install:
	go install dave
	go install dave/sdave

clean:
	rm -f $(BINARY) sdave/sdave
