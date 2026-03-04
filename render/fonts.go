package render

import "golang.org/x/image/font/gofont/gomono"

// GoMonoTTF is the embedded Go Mono TTF font data.
// Used as the default font when no font is specified in config.
var GoMonoTTF []byte = gomono.TTF
