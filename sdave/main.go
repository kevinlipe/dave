// sdave — stream dave, a non-interactive stream editor using Dave's command language.
//
// Usage:
//
//	sdave [-n] [-e script] [-f scriptfile] [file ...]
//
// Without -e or -f, the first positional argument is taken as the script.
// With no file arguments, sdave reads from standard input.
// Without -n, the entire buffer is printed to stdout after the script runs.
package main

import (
	"bufio"
	"dave/buffer"
	"dave/cmd"
	"dave/file"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	suppress := flag.Bool("n", false, "suppress default output")
	scriptFile := flag.String("f", "", "read script from file")
	var scripts []string
	flag.Func("e", "script to execute (may be repeated)", func(s string) error {
		scripts = append(scripts, s)
		return nil
	})
	flag.Parse()
	args := flag.Args()

	// Load script from -f if given.
	if *scriptFile != "" {
		data, err := os.ReadFile(*scriptFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sdave: %v\n", err)
			os.Exit(1)
		}
		scripts = append(scripts, string(data))
	}

	// If no -e or -f, treat the first positional argument as the script.
	script := strings.Join(scripts, "\n")
	if script == "" {
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "sdave: no script specified")
			flag.Usage()
			os.Exit(1)
		}
		script = args[0]
		args = args[1:]
	}

	// Process each input file, or stdin if none given.
	if len(args) == 0 {
		if err := processReader(os.Stdout, os.Stdin, script, *suppress); err != nil {
			fmt.Fprintf(os.Stderr, "sdave: %v\n", err)
			os.Exit(1)
		}
		return
	}
	for _, name := range args {
		if err := processFile(os.Stdout, name, script, *suppress); err != nil {
			fmt.Fprintf(os.Stderr, "sdave: %s: %v\n", name, err)
			os.Exit(1)
		}
	}
}

// processFile loads name from disk, runs script against it, and writes the
// result to w unless suppress is true.
func processFile(w io.Writer, name, script string, suppress bool) error {
	f := file.NewNamed(name)
	if err := f.EnsureLoaded(); err != nil {
		return err
	}
	return process(w, f, script, suppress)
}

// processReader reads all of r into a fresh buffer, runs script against it,
// and writes the result to w unless suppress is true.
func processReader(w io.Writer, r io.Reader, script string, suppress bool) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}
	f := file.New()
	content := f.DecodeContent(string(data))
	f.Buffer().Insert(0, content)
	return process(w, f, script, suppress)
}

// process runs script against f in a fresh session, then writes the buffer
// to w unless suppress is true.
//
// The destination is a parameter rather than os.Stdout so the whole pipeline
// is testable; cmd.NewSession already takes a writer for the same reason.
//
// Like ssam, the initial dot is set to the whole file so that commands like
// s/re/repl/ work without an explicit address.
func process(w io.Writer, f *file.File, script string, suppress bool) error {
	// Set dot to the whole file before running the script.
	f.SetDot(buffer.Range{Start: 0, End: f.Len()})

	// The script's own output and the buffer dump share one buffered writer,
	// so they stay in order and neither costs a syscall per line: the buffer
	// streams out in gap segments, and a CRLF file a line at a time.
	bw := bufio.NewWriter(w)
	s := cmd.NewSession(bw)
	s.AddFile(f)
	err := s.ExecString(script)
	if err == nil && !suppress {
		err = f.WriteContent(bw)
	}
	// Flush whatever the script managed to print, even on the way out.
	if ferr := bw.Flush(); err == nil {
		err = ferr
	}
	return err
}
