# Dave build rules.
#
# Targets:
#   all     - build dave.exe (dynamically linked, requires SDL2.dll at runtime)
#   static  - build self-contained dave.exe with SDL2 statically linked
#   clean   - remove build artifacts
#
# The 'static' target needs the MSYS2 ucrt64 static SDL2 library.
# SDL2_LIBDIR is auto-detected from common install locations; override if needed:
#
#   make static SDL2_LIBDIR=/c/msys64/ucrt64/lib

BINARY = dave.exe

# Auto-detect the MSYS2 ucrt64 lib dir by looking for libSDL2.a in common locations.
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

.PHONY: all static clean

all:
	go build -o $(BINARY) .

static:
	CGO_LDFLAGS="$(SDL2_STATIC_LDFLAGS)" go build -o $(BINARY) .

clean:
	rm -f $(BINARY)
