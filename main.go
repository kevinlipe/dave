// test of writing to file

package main

import (
	"bufio"
	"dave/buffer"
	"dave/cmd"
	"dave/config"
	"dave/file"
	"dave/input"
	"dave/menu"
	"dave/pane"
	"dave/render"
	"errors"
	"fmt"
	"os"

	"github.com/veandco/go-sdl2/sdl"
)

func main() {
	args := os.Args[1:]
	headless := false
	var files []string
	for _, a := range args {
		if a == "-d" {
			headless = true
		} else {
			files = append(files, a)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dave: config: %v\n", err)
		cfg = config.Default()
	}

	s := cmd.NewSession(os.Stdout)
	for _, name := range files {
		f := file.NewNamed(name)
		s.AddFile(f)
	}

	if headless {
		runREPL(s)
		return
	}

	if err := runGUI(cfg, s); err != nil {
		fmt.Fprintln(os.Stderr, "dave:", err)
		os.Exit(1)
	}
}

// runGUI opens the SDL window and runs the main event loop.
func runGUI(cfg *config.Config, sess *cmd.Session) error {
	win, err := render.Open(cfg)
	if err != nil {
		return err
	}
	defer win.Close()

	pal := win.Palette
	face := win.Face

	cmdWin := input.NewCmdWindow()
	handler := input.NewHandler(sess, cmdWin, face, pal)

	// Print menu lines for files loaded from command-line arguments.
	for _, f := range sess.Files {
		cmdWin.AppendOutput(f.MenuLine(f == sess.Current) + "\n")
	}

	// Start with no panes open. Files are in sess.Files but no view is created
	// until the user picks one from the buffer menu (right-click), exactly as Sam.
	var panes []*pane.Pane
	handler.SetPanes(panes)

	lineH := int32(face.Height())
	cmdBorder := &pane.Border{
		Y:            int32(cfg.CommandLines) * lineH,
		MinAbove:     lineH,
		MinBelow:     lineH,
		MaxAboveFrac: 0.4,
	}
	dragger := input.NewBorderDragger(cmdBorder)
	defer dragger.Close()

	var activeMenu *menu.Menu

	// drawFrame renders the current state to the window.
	drawFrame := func() {
		winW, winH := win.Size()
		cmdRect, paneRects := layout(winW, winH, cmdBorder.Y, len(panes))

		// Relayout all panes (also triggers EnsureLoaded if needed).
		for i, p := range panes {
			if i < len(paneRects) {
				p.Relayout(paneRects[i].W - render.ScrollBarW)
			}
		}

		win.Ren.SetDrawColor(pal.Background.R, pal.Background.G, pal.Background.B, 255)
		win.Ren.Clear()

		// Body area: white when no panes open (matching Sam's startup appearance).
		if len(panes) == 0 {
			win.Ren.SetDrawColor(255, 255, 255, 255)
			win.Ren.FillRect(&sdl.Rect{X: 0, Y: cmdRect.H, W: winW, H: winH - cmdRect.H})
		}

		// Command window.
		cmdWin.Draw(win.Ren, cmdRect, face, pal, handler.ActivePane() == nil)

		// File panes.
		for i, p := range panes {
			if i < len(paneRects) {
				active := handler.ActivePane() == p
				p.Draw(win.Ren, paneRects[i], active)
			}
		}

		// Vertical divider between two panes.
		if len(panes) == 2 {
			mid := paneRects[0].X + paneRects[0].W
			win.Ren.SetDrawColor(pal.Border.R, pal.Border.G, pal.Border.B, 255)
			win.Ren.FillRect(&sdl.Rect{X: mid, Y: cmdRect.H, W: 1, H: winH - cmdRect.H})
		}

		// Menu overlay (drawn last, on top of everything).
		if activeMenu != nil {
			activeMenu.Draw(win.Ren, face, pal)
		}

		win.Ren.Present()
	}

	// Initial paint before blocking on events.
	drawFrame()

	for {
		// Block until at least one event arrives; avoids busy-spinning at idle.
		event := sdl.WaitEvent()
		if event == nil {
			continue
		}

		winW, winH := win.Size()
		cmdRect, paneRects := layout(winW, winH, cmdBorder.Y, len(panes))

		// Process the event from WaitEvent, then drain any queued events.
		for ; event != nil; event = sdl.PollEvent() {
			switch ev := event.(type) {
			case *sdl.QuitEvent:
				return nil

			case *sdl.WindowEvent:
				if ev.Event == sdl.WINDOWEVENT_SIZE_CHANGED {
					win.UpdateSize()
				}

			case *sdl.KeyboardEvent:
				if ev.Type == sdl.KEYDOWN {
					if err := handler.HandleKey(ev, winW, winH); errors.Is(err, cmd.ErrQuit) {
						return nil
					}
				}

			case *sdl.TextInputEvent:
				if activeMenu == nil {
					handler.HandleText(ev)
				}

			case *sdl.MouseButtonEvent:
				if activeMenu != nil {
					if ev.Type == sdl.MOUSEBUTTONUP {
						res, _ := activeMenu.HandleMouseUp(ev.X, ev.Y, face)
						activeMenu = nil
						panes = applyMenuResult(res, sess, panes, handler, cfg, pal, face)
					}
					continue
				}
				if ev.Type == sdl.MOUSEBUTTONDOWN {
					if dragger.MouseDown(ev.Button, ev.Y) {
						continue
					}
					handler.HandleMouseDown(ev, cmdRect, paneRects)
					mod := sdl.GetModState()
					// Shift+right or middle → editing menu.
					if ev.Button == sdl.BUTTON_MIDDLE ||
						(ev.Button == sdl.BUTTON_RIGHT && mod&sdl.KMOD_SHIFT != 0) {
						activeMenu = menu.NewEditMenu(ev.X, ev.Y)
					} else if ev.Button == sdl.BUTTON_RIGHT {
						// Plain right-click → buffer menu.
						activeMenu = menu.NewBufferMenu(sess.Files, len(panes) > 1, ev.X, ev.Y)
					}
					if activeMenu != nil {
						activeMenu.Clamp(winW, winH, face)
					}
				} else if ev.Type == sdl.MOUSEBUTTONUP {
					if dragger.MouseUp() {
						continue
					}
					handler.HandleMouseUp(ev)
				}

			case *sdl.MouseMotionEvent:
				if activeMenu != nil {
					activeMenu.HandleMouseMotion(ev.X, ev.Y, face)
					continue
				}
				if dragger.MouseMotion(ev.Y, winH) {
					continue
				}
				handler.HandleMouseMotion(ev, cmdRect, paneRects)

			case *sdl.MouseWheelEvent:
				handler.HandleScroll(ev)
			}
		}

		drawFrame()
	}
}

// layout computes bounding rects for the command window and file panes.
func layout(winW, winH, cmdH int32, nPanes int) (cmdRect sdl.Rect, paneRects []sdl.Rect) {
	cmdRect = sdl.Rect{X: 0, Y: 0, W: winW, H: cmdH}
	bodyY := cmdH
	bodyH := winH - bodyY

	switch nPanes {
	case 0:
		// No panes: body area left empty (white background drawn above).
	case 1:
		paneRects = []sdl.Rect{{X: 0, Y: bodyY, W: winW, H: bodyH}}
	case 2:
		half := winW / 2
		paneRects = []sdl.Rect{
			{X: 0, Y: bodyY, W: half, H: bodyH},
			{X: half + 1, Y: bodyY, W: winW - half - 1, H: bodyH},
		}
	}
	return
}

// applyMenuResult handles a selection from the buffer or edit menu.
// Returns the (possibly updated) panes slice.
func applyMenuResult(res menu.Result, sess *cmd.Session, panes []*pane.Pane, handler *input.Handler, cfg *config.Config, pal render.Palette, face render.Face) []*pane.Pane {
	switch res.Action {
	case "new":
		f := file.New()
		sess.AddFile(f)
		if len(panes) == 0 {
			panes = append(panes, pane.New(f, cfg, pal, face))
		} else {
			ap := handler.ActivePane()
			if ap == nil {
				ap = panes[0]
			}
			ap.SetFile(f)
		}
	case "sidebar":
		if len(panes) < 2 {
			f := sess.Current
			if f == nil {
				f = file.New()
				sess.AddFile(f)
			}
			panes = append(panes, pane.New(f, cfg, pal, face))
		}
	case "kill sidebar":
		if len(panes) > 1 {
			panes = panes[:1]
		}
	case "close":
		ap := handler.ActivePane()
		for i, p := range panes {
			if p == ap {
				panes = append(panes[:i], panes[i+1:]...)
				break
			}
		}
		if len(panes) == 0 {
			panes = nil
		}
	case "snarf":
		if handler.IsCmdWinActive() {
			handler.CmdWin().Snarf()
		} else if p := handler.ActivePane(); p != nil {
			dot := p.File.Dot()
			if dot.Start != dot.End {
				sdl.SetClipboardText(p.File.Buffer().ReadAt(dot.Start, dot.End-dot.Start))
			}
		}
	case "cut":
		if handler.IsCmdWinActive() {
			handler.CmdWin().Cut()
		} else if p := handler.ActivePane(); p != nil {
			f := p.File
			dot := f.Dot()
			if dot.Start != dot.End {
				sdl.SetClipboardText(f.Buffer().ReadAt(dot.Start, dot.End-dot.Start))
				sess.ApplyEdit(f, dot.Start, dot.End-dot.Start, "")
				f.SetDot(buffer.Range{Start: dot.Start, End: dot.Start})
			}
		}
	case "paste":
		if handler.IsCmdWinActive() {
			handler.CmdWin().Paste()
		} else if p := handler.ActivePane(); p != nil {
			text, err := sdl.GetClipboardText()
			if err == nil && text != "" {
				f := p.File
				dot := f.Dot()
				del := dot.End - dot.Start
				sess.ApplyEdit(f, dot.Start, del, text)
				n := len([]rune(text))
				f.SetDot(buffer.Range{Start: dot.Start + n, End: dot.Start + n})
			}
		}
	case "write":
		ap := handler.ActivePane()
		if ap != nil {
			_ = ap.File.Save()
		}
	case "undo":
		sess.DoUndo()
	}

	if res.File != nil {
		sess.Current = res.File
		ap := handler.ActivePane()
		if ap == nil {
			// No pane open yet: create one for this file.
			if len(panes) == 0 {
				panes = append(panes, pane.New(res.File, cfg, pal, face))
			} else {
				panes[0].SetFile(res.File)
			}
		} else {
			ap.SetFile(res.File)
		}
	}

	handler.SetPanes(panes)
	return panes
}

// ---- headless (-d) mode -----------------------------------------------------

func runREPL(s *cmd.Session) {
	scanner := bufio.NewScanner(os.Stdin)
	var accum string
	var state cmd.InputState

	for {
		if !scanner.Scan() {
			if accum != "" {
				execAccum(s, accum)
			}
			break
		}
		line := scanner.Text()
		if state.IsReady() && line == "" {
			continue
		}
		accum += line + "\n"
		complete := state.Feed(line)
		if !complete {
			continue
		}
		if quit := execAccum(s, accum); quit {
			return
		}
		accum = ""
		state.Reset()
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "dave: %v\n", err)
	}
}

func execAccum(s *cmd.Session, input string) (quit bool) {
	err := s.ExecString(input)
	if err != nil {
		if errors.Is(err, cmd.ErrQuit) {
			return true
		}
		fmt.Fprintf(os.Stderr, "? %v\n", err)
	}
	return false
}
