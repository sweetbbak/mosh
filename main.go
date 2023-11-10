package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"

	// "time"
	"unsafe"
)

const VERSION = "0.0.1"

const (
	BACKSPACE   = 127
	ARROW_LEFT  = 1000 + iota
	ARROW_RIGHT = 1000 + iota
	ARROW_UP    = 1000 + iota
	ARROW_DOWN  = 1000 + iota
	DEL_KEY     = 1000 + iota
	HOME_KEY    = 1000 + iota
	END_KEY     = 1000 + iota
	PAGE_UP     = 1000 + iota
	PAGE_DOWN   = 1000 + iota
)

type Termios struct {
	Iflag  uint32
	Oflag  uint32
	Cflag  uint32
	Lflag  uint32
	Cc     [20]byte
	Ispeed uint32
	Ospeed uint32
}

type WinSize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

type Terminal struct {
	cx          int
	cy          int
	buf         bytes.Buffer
	cmd         bytes.Buffer
	rows        []erow
	numRows     int
	coloff      int
	rowoff      int
	screenRows  int
	screenCols  int
	origTermios *Termios
}

var E Terminal

type erow struct {
	idx    int
	size   int
	rsize  int
	chars  []byte
	render []byte
}

func die(err error) {
	disableRawMode()
	io.WriteString(os.Stdout, "\x1b[2J")
	io.WriteString(os.Stdout, "\x1b[H")
	log.Fatal(err)
}

func TcSetAttr(fd uintptr, termios *Termios) error {
	// TCSETS+1 == TCSETSW, because TCSAFLUSH doesn't exist
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TCSETS+1), uintptr(unsafe.Pointer(termios))); err != 0 {
		return err
	}
	return nil
}

func TcGetAttr(fd uintptr) *Termios {
	var termios = &Termios{}
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(termios))); err != 0 {
		log.Fatalf("Problem getting terminal attributes: %s\n", err)
	}
	return termios
}

func enableRawMode() {
	E.origTermios = TcGetAttr(os.Stdin.Fd())
	var raw Termios
	raw = *E.origTermios
	raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Cflag |= syscall.CS8
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	raw.Cc[syscall.VMIN+1] = 0
	raw.Cc[syscall.VTIME+1] = 1
	if e := TcSetAttr(os.Stdin.Fd(), &raw); e != nil {
		log.Fatalf("Problem enabling raw mode: %s\n", e)
	}
}

func disableRawMode() {
	if e := TcSetAttr(os.Stdin.Fd(), E.origTermios); e != nil {
		log.Fatalf("Problem disabling raw mode: %s\n", e)
	}
}

func editorReadKey() int {
	var buffer [1]byte
	var cc int
	var err error
	for cc, err = os.Stdin.Read(buffer[:]); cc != 1; cc, err = os.Stdin.Read(buffer[:]) {
	}
	if err != nil {
		die(err)
	}
	if buffer[0] == '\x1b' {
		var seq [2]byte
		if cc, _ = os.Stdin.Read(seq[:]); cc != 2 {
			return '\x1b'
		}

		if seq[0] == '[' {
			if seq[1] >= '0' && seq[1] <= '9' {
				if cc, err = os.Stdin.Read(buffer[:]); cc != 1 {
					return '\x1b'
				}
				if buffer[0] == '~' {
					switch seq[1] {
					case '1':
						return HOME_KEY
					case '3':
						return DEL_KEY
					case '4':
						return END_KEY
					case '5':
						return PAGE_UP
					case '6':
						return PAGE_DOWN
					case '7':
						return HOME_KEY
					case '8':
						return END_KEY
					}
				}
				// XXX - what happens here?
			} else {
				switch seq[1] {
				case 'A':
					return ARROW_UP
				case 'B':
					return ARROW_DOWN
				case 'C':
					return ARROW_RIGHT
				case 'D':
					return ARROW_LEFT
				case 'H':
					return HOME_KEY
				case 'F':
					return END_KEY
				}
			}
		} else if seq[0] == '0' {
			switch seq[1] {
			case 'H':
				return HOME_KEY
			case 'F':
				return END_KEY
			}
		}

		return '\x1b'
	}
	return int(buffer[0])
}

func getCursorPosition(rows *int, cols *int) int {
	io.WriteString(os.Stdout, "\x1b[6n")
	var buffer [1]byte
	var buf []byte
	var cc int
	for cc, _ = os.Stdin.Read(buffer[:]); cc == 1; cc, _ = os.Stdin.Read(buffer[:]) {
		if buffer[0] == 'R' {
			break
		}
		buf = append(buf, buffer[0])
	}
	if string(buf[0:2]) != "\x1b[" {
		log.Printf("Failed to read rows;cols from tty\n")
		return -1
	}
	if n, e := fmt.Sscanf(string(buf[2:]), "%d;%d", rows, cols); n != 2 || e != nil {
		if e != nil {
			log.Printf("getCursorPosition: fmt.Sscanf() failed: %s\n", e)
		}
		if n != 2 {
			log.Printf("getCursorPosition: got %d items, wanted 2\n", n)
		}
		return -1
	}
	return 0
}

func getWindowSize(rows *int, cols *int) int {
	var w WinSize
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL,
		os.Stdout.Fd(),
		syscall.TIOCGWINSZ,
		uintptr(unsafe.Pointer(&w)),
	)
	if err != 0 { // type syscall.Errno
		io.WriteString(os.Stdout, "\x1b[999C\x1b[999B")
		return getCursorPosition(rows, cols)
	} else {
		*rows = int(w.Row)
		*cols = int(w.Col)
		return 0
	}
	return -1
}

func initEd() {
	if getWindowSize(&E.screenRows, &E.screenCols) == -1 {
		die(fmt.Errorf("Couldn't get screen size"))
	}
	E.screenRows -= 1
}

func editorStatus(ab *bytes.Buffer) {
	ab.WriteString("\x1b[7m")
	status := fmt.Sprintf("INSERT| xxx")
	ln := len(status)
	if ln > E.screenCols {
		ln = E.screenCols
	}
	// write status but dont overflow / wrap around
	ab.WriteString(status[:ln])
	ab.WriteString("\x1b[m")
	ab.WriteString("\r\n")

}

func editorDrawRows(ab *bytes.Buffer) {
	pad := "~ "
	for y := 0; y < E.screenRows; y++ {
		if y >= E.numRows {
		}
		ab.WriteString(pad)
		// ab.WriteString("\x1b[38;5;255;48;5;241m" + strings.Repeat(" ", E.screenCols-3))
		ab.WriteString("\x1b[32m")
		ab.WriteString("\x1b[K")
		ab.WriteString("\r\n")
	}
}

func editorRefreshScreen() {
	// ab := bytes.NewBufferString("\x1b[25l")
	ab := bytes.NewBufferString("")
	ab.WriteString("\r\n")
	// ab.WriteString("\x1b[H")
	// ab.WriteString("\x1b[2K")
	// ab.WriteString("\x1b[0;0H")
	// ab.WriteString("\x1b[?25h")
	prompt := "[\x1b[36msweet\x1b[0m]-$ "
	ab.WriteString("\x1b[H")
	ab.WriteString("\x1b[2K")
	ab.WriteString(prompt)
	ab.WriteString(E.buf.String())
	// editorDrawRows(ab)
	// editorStatus(ab)
	_, e := ab.WriteTo(os.Stdout)
	if e != nil {
		log.Fatal(e)
	}

	// cmd := &E.cmd
	// _, c := cmd.WriteTo(os.Stdout)
	// if c != nil {
	// 	log.Fatal(e)
	// }

}

func editProcessKeypress(buf *bytes.Buffer, cmd *bytes.Buffer) {
	// buf := E.buf
	c := editorReadKey()
	switch c {
	case '\r':
		// buf.WriteString("\x1b[2K")
		// buf.WriteString("\x1b[H\x1b[2J")
		fmt.Println("\r")
		execute(cmd)
		buf.Reset()
		cmd.Reset()
	case ('q' & 0x1f):
		disableRawMode()
		os.Exit(0)
	case HOME_KEY:
		// fmt.Printf("\x1b[H")
		// fmt.Printf("\x1b[K")
		buf.WriteString("\x1b[H")
		buf.WriteString("\x1b[K")
	case END_KEY:
	case ('h' & 0x1f), BACKSPACE, DEL_KEY:
		if cmd.Len() > 0 {
			cmd.Truncate(cmd.Len() - 1)
			buf.Truncate(buf.Len() - 1)
		}

	case ARROW_UP, ARROW_DOWN, ARROW_LEFT, ARROW_RIGHT:
		buf.WriteString("\x1b[A")
	case ('l' & 0x1f):
		break
	case '\x1b':
		disableRawMode()
		os.Exit(0)
	default:
		buf.WriteString(fmt.Sprintf("%c", c))
		cmd.WriteString(fmt.Sprintf("%c", c))

	}
}

func clearScreen() {
	fmt.Printf("\x1b[H\x1b[2J")
}

func execute(buf *bytes.Buffer) {
	fmt.Printf("\x1b[H\x1b[2J")
	fmt.Println()
	disableRawMode()

	str := buf.String()
	cmd := strings.Split(str, " ")
	cmdx := exec.Command(cmd[0], cmd[1:]...)
	cmdx.Stdout = os.Stdout
	cmdx.Stderr = os.Stderr
	cmdx.Stdin = os.Stdin
	err := cmdx.Run()
	if err != nil {
		fmt.Println(err)
	}

	enableRawMode()
}

func main() {
	enableRawMode()
	defer disableRawMode()
	initEd()
	clearScreen()

	buf := &E.buf
	cmd := &E.cmd
	// var x bytes.Buffer
	// x.Reset()
	// x.UnreadRune()
	for {
		editorRefreshScreen()
		editProcessKeypress(buf, cmd)
	}
	// getWindowSize()
	// os.Exit(0)
}
