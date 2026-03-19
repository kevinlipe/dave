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
		cmdWin.AppendOutput(f.MenuLine(f == sess.Current, false) + "\n")
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
				// Use CurrentFilePane (not ActivePane) so the current pane keeps
				// its heavy border even when the command window has keyboard focus.
				current := handler.CurrentFilePane() == p
				p.Draw(win.Ren, paneRects[i], current)
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

	// Initial paint before blocking on events. Show the window only after
	// the first frame is ready to avoid a blank-window flash on startup.
	drawFrame()
	win.Win.Show()

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
				win.Win.Hide()
				return nil

			case *sdl.WindowEvent:
				if ev.Event == sdl.WINDOWEVENT_SIZE_CHANGED {
					win.UpdateSize()
				}

			case *sdl.KeyboardEvent:
				if ev.Type == sdl.KEYDOWN {
					wasEnter := ev.Keysym.Sym == sdl.K_RETURN || ev.Keysym.Sym == sdl.K_KP_ENTER
					if err := handler.HandleKey(ev, winW, winH); errors.Is(err, cmd.ErrQuit) {
						win.Win.Hide()
						return nil
					}
					if handler.IsCmdWinActive() {
						handler.CmdWin().EnsureVisible(lineH, cmdRect.H)
						// After executing a command, scroll the pane showing the
						// current file to its (possibly updated) dot.
						if wasEnter {
							for _, p := range panes {
								if p.File == sess.Current {
									p.EnsureVisible(sess.Current.Dot().Start, winH)
									break
								}
							}
						}
					}
					// B command requests a pane when none is open.
					if f := sess.OpenRequest; f != nil && len(panes) == 0 {
						sess.OpenRequest = nil
						panes = append(panes, pane.New(f, cfg, pal, face))
						handler.SetPanes(panes)
					} else {
						sess.OpenRequest = nil
					}

					// Config command: reload config file and reapply all preferences.
					if sess.ConfigRequest {
						sess.ConfigRequest = false
						msg := applyConfig(win, cfg, &pal, &face, panes, handler, cmdBorder)
						lineH = int32(face.Height())
						cmdWin.AppendOutput(msg)
						// EnsureVisible ran before AppendOutput moved the cursor;
						// call it again so the output is actually in view.
						cmdWin.EnsureVisible(lineH, cmdRect.H)
					}
				}

			case *sdl.TextInputEvent:
				if activeMenu == nil {
					handler.HandleText(ev)
					if handler.IsCmdWinActive() {
						handler.CmdWin().EnsureVisible(lineH, cmdRect.H)
					}
				}

			case *sdl.MouseButtonEvent:
				if activeMenu != nil {
					if ev.Type == sdl.MOUSEBUTTONUP {
						res, _ := activeMenu.HandleMouseUp(ev.X, ev.Y, face)
						activeMenu = nil
						panes = applyMenuResult(res, sess, panes, handler, cmdWin, cfg, pal, face, winH)
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
						inPane := make(map[*file.File]bool, len(panes))
						for _, p := range panes {
							inPane[p.File] = true
						}
						scaleLabel := plan9ScaleLabel(face, cfg.Plan9Scale)
						// Only pass activeFile if it is tracked in the session;
						// an empty sidebar placeholder must not appear in the list.
						var activeFile *file.File
						if ap := handler.ActivePane(); ap != nil {
							for _, f := range sess.Files {
								if f == ap.File {
									activeFile = ap.File
									break
								}
							}
						}
						activeMenu = menu.NewBufferMenu(sess.Files, inPane, len(panes) > 1, scaleLabel, activeFile, ev.X, ev.Y)
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
func applyMenuResult(res menu.Result, sess *cmd.Session, panes []*pane.Pane, handler *input.Handler, cmdWin *input.CmdWindow, cfg *config.Config, pal render.Palette, face render.Face, winH int32) []*pane.Pane {
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
	case "pane":
		if len(panes) < 2 {
			// Open a second pane showing the current file.
			var f *file.File
			if ap := handler.ActivePane(); ap != nil {
				f = ap.File
			}
			panes = append(panes, pane.New(f, cfg, pal, face))
		}
	case "kill pane":
		if len(panes) > 1 {
			// Close the active pane and promote the other to sole pane.
			ap := handler.ActivePane()
			if ap == panes[0] {
				panes = panes[1:]
			} else {
				panes = panes[:1]
			}
		}
	case "close":
		ap := handler.ActivePane()
		if ap != nil && ap.File != nil {
			// Only remove from session if no other pane still has this file.
			fileInOtherPane := false
			for _, p := range panes {
				if p != ap && p.File == ap.File {
					fileInOtherPane = true
					break
				}
			}
			if fileInOtherPane {
				ap.SetFile(nil)
			} else if err := sess.RemoveFile(ap.File, false); err != nil {
				cmdWin.AppendOutput(err.Error() + "\n")
			} else {
				// Clear the pane; it stays open but empty (white, no header).
				ap.SetFile(nil)
			}
		}
	case "snarf":
		if handler.IsCmdWinActive() {
			handler.CmdWin().Snarf()
		} else if p := handler.ActivePane(); p != nil && p.File != nil {
			dot := p.File.Dot()
			if dot.Start != dot.End {
				sdl.SetClipboardText(p.File.Buffer().ReadAt(dot.Start, dot.End-dot.Start))
			}
		}
	case "cut":
		if handler.IsCmdWinActive() {
			handler.CmdWin().Cut()
		} else if p := handler.ActivePane(); p != nil && p.File != nil {
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
		} else if p := handler.ActivePane(); p != nil && p.File != nil {
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
		if ap != nil && ap.File != nil {
			_ = ap.File.Save()
		}
	case "undo":
		sess.DoUndo()
	case "scale font", "unscale font":
		if p9, ok := face.(*render.Plan9Face); ok {
			if p9.Scale() == 1.0 {
				p9.SetScale(cfg.Plan9Scale)
			} else {
				p9.SetScale(1.0)
			}
		}
	case "look":
		ap := handler.ActivePane()
		if ap != nil && ap.File != nil {
			if err := ap.File.EnsureLoaded(); err != nil {
				break
			}
			f := ap.File
			dot := f.Dot()
			buf := f.Buffer()

			// Needle: use selection if non-null, else word under cursor.
			var needle string
			if dot.Start != dot.End {
				needle = buf.ReadAt(dot.Start, dot.End-dot.Start)
			} else {
				lo, hi := input.WordBounds(buf, dot.Start)
				needle = buf.ReadAt(lo, hi-lo)
			}
			if needle != "" {
				if found, end := lookForward(buf, needle, dot.End); found >= 0 {
					f.SetDot(buffer.Range{Start: found, End: end})
					ap.EnsureVisible(found, winH)
				}
			}
		}
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

// lookForward searches buf for the first occurrence of needle at or after
// start (codepoint offset), wrapping around to 0 if not found. Returns the
// start and end codepoint offsets of the match, or (-1, -1) if not found.
func lookForward(buf interface{ String() string; Len() int }, needle string, start int) (int, int) {
	runes := []rune(buf.String())
	n := len(runes)
	needleRunes := []rune(needle)
	nLen := len(needleRunes)
	if nLen == 0 || nLen > n {
		return -1, -1
	}
	// Search from start to end of buffer, then wrap from 0 to start.
	for _, from := range []int{start, 0} {
		limit := n - nLen + 1
		if from == 0 {
			// Wrap: only search up to original start to avoid re-matching.
			if limit > start {
				limit = start
			}
		}
		for i := from; i < limit; i++ {
			match := true
			for j := 0; j < nLen; j++ {
				if runes[i+j] != needleRunes[j] {
					match = false
					break
				}
			}
			if match {
				return i, i + nLen
			}
		}
	}
	return -1, -1
}

// plan9ScaleLabel returns "scale font" or "unscale font" when face is a
// Plan9Face and configScale differs from 1.0, so the menu item is only
// shown for Plan 9 fonts where toggling has a visible effect.
func plan9ScaleLabel(face render.Face, configScale float64) string {
	p9, ok := face.(*render.Plan9Face)
	if !ok || configScale == 1.0 {
		return ""
	}
	if p9.Scale() == 1.0 {
		return "scale font"
	}
	return "unscale font"
}

// applyConfig reloads the config file and pushes all new values into the
// running GUI. cfg is updated in-place so all panes that hold a *Config
// pointer pick up the new values automatically. Returns a one-line status
// message suitable for appending to the command window.
func applyConfig(win *render.Window, cfg *config.Config, pal *render.Palette, face *render.Face, panes []*pane.Pane, handler *input.Handler, cmdBorder *pane.Border) string {
	newCfg, err := config.Load()
	if err != nil {
		return "Config: " + err.Error() + "\n"
	}

	// Reload the font face whenever font-related settings may have changed.
	newFace, err := win.ReloadFace(newCfg)
	if err != nil {
		// Keep the old face; still apply everything else.
		newCfg.Font = cfg.Font
		newCfg.FontSize = cfg.FontSize
		newCfg.FontMode = cfg.FontMode
		newCfg.Plan9Font = cfg.Plan9Font
		newCfg.Plan9Scale = cfg.Plan9Scale
	} else {
		(*face).Close()
		*face = newFace
		handler.SetFace(newFace)
		for _, p := range panes {
			p.SetFace(newFace)
		}
	}

	// Apply new values into the existing *Config so panes see them via their pointer.
	*cfg = *newCfg

	// Rebuild and push the new palette.
	newPal := render.PaletteFromConfig(cfg)
	*pal = newPal
	win.Palette = newPal
	handler.SetPalette(newPal)
	for _, p := range panes {
		p.SetPalette(newPal)
	}

	// Update command-window height from command_lines (respects the new line height).
	lineH := int32((*face).Height())
	cmdBorder.Y = int32(cfg.CommandLines) * lineH
	cmdBorder.MinAbove = lineH
	cmdBorder.MinBelow = lineH

	if err != nil {
		return "Config reloaded (font error: " + err.Error() + ")\n"
	}
	return "Config reloaded.\n"
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
