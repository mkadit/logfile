// logfile/stacktrace.go - Error stack trace functionality
package logfile

import (
	"errors"
	"fmt"
	"io"
	"log"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Frame represents a program counter inside a stack frame
type Frame uintptr

// pc returns the program counter for this frame
func (f Frame) pc() uintptr { return uintptr(f) - 1 }

// file returns the full path to the file for this Frame's pc
func (f Frame) file() string {
	fn := runtime.FuncForPC(f.pc())
	if fn == nil {
		return "unknown"
	}
	file, _ := fn.FileLine(f.pc())
	return file
}

// line returns the line number of source code
func (f Frame) line() int {
	fn := runtime.FuncForPC(f.pc())
	if fn == nil {
		return 0
	}
	_, line := fn.FileLine(f.pc())
	return line
}

// name returns the name of this function
func (f Frame) name() string {
	fn := runtime.FuncForPC(f.pc())
	if fn == nil {
		return "unknown"
	}
	return fn.Name()
}

// Format formats the frame according to fmt.Formatter interface
func (f Frame) Format(s fmt.State, verb rune) {
	var err error
	switch verb {
	case 's':
		switch {
		case s.Flag('+'):
			_, err = io.WriteString(s, f.name())
			if err != nil {
				log.Printf("Error writing name: %v", err) // Log the error
				return
			}
			_, err = io.WriteString(s, "\n\t")
			if err != nil {
				log.Printf("Error writing newline: %v", err) // Log the error
				return
			}
			_, err = io.WriteString(s, f.file())
			if err != nil {
				log.Printf("Error writing file: %v", err) // Log the error
				return
			}
		default:
			_, err = io.WriteString(s, path.Base(f.file()))
			if err != nil {
				log.Printf("Error writing file base name: %v", err) // Log the error
				return
			}
		}
	case 'd':
		_, err = io.WriteString(s, strconv.Itoa(f.line()))
		if err != nil {
			log.Printf("Error writing line number: %v", err) // Log the error
			return
		}
	case 'n':
		_, err = io.WriteString(s, funcname(f.name()))
		if err != nil {
			log.Printf("Error writing function name: %v", err) // Log the error
			return
		}
	case 'v':
		f.Format(s, 's')
		_, err = io.WriteString(s, ":")
		if err != nil {
			log.Printf("Error writing colon: %v", err) // Log the error
			return
		}
		f.Format(s, 'd')
	}
}

// MarshalText formats a stacktrace Frame as text
func (f Frame) MarshalText() ([]byte, error) {
	name := f.name()
	if name == "unknown" {
		return []byte(name), nil
	}
	return []byte(fmt.Sprintf("%s %s:%d", name, f.file(), f.line())), nil
}

// StackTrace is stack of Frames from innermost to outermost
type StackTrace []Frame

// Format formats the stack of Frames
func (st StackTrace) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v':
		switch {
		case s.Flag('+'):
			for _, f := range st {
				io.WriteString(s, "\n")
				f.Format(s, verb)
			}
		case s.Flag('#'):
			fmt.Fprintf(s, "%#v", []Frame(st))
		default:
			st.formatSlice(s, verb)
		}
	case 's':
		st.formatSlice(s, verb)
	}
}

// formatSlice formats StackTrace as a slice of Frame
func (st StackTrace) formatSlice(s fmt.State, verb rune) {
	io.WriteString(s, "[")
	for i, f := range st {
		if i > 0 {
			io.WriteString(s, " ")
		}
		f.Format(s, verb)
	}
	io.WriteString(s, "]")
}

// stack represents a stack of program counters
type stack []uintptr

// Format formats the stack according to fmt.Formatter interface
func (s *stack) Format(st fmt.State, verb rune) {
	switch verb {
	case 'v':
		switch {
		case st.Flag('+'):
			for _, pc := range *s {
				f := Frame(pc)
				fmt.Fprintf(st, "\n%+v", f)
			}
		}
	}
}

// StackTrace returns the StackTrace for this stack
func (s *stack) StackTrace() StackTrace {
	f := make([]Frame, len(*s))
	for i := 0; i < len(f); i++ {
		f[i] = Frame((*s)[i])
	}
	return f
}

// callersMu protects the callers function
var callersMu sync.Mutex

// callers returns a stack of program counters
func callers() *stack {
	callersMu.Lock()
	defer callersMu.Unlock()

	const depth = 32
	var pcs [depth]uintptr
	n := runtime.Callers(3, pcs[:])
	var st stack = pcs[0:n]
	return &st
}

// funcname removes the path prefix component of a function's name
func funcname(name string) string {
	i := strings.LastIndex(name, "/")
	name = name[i+1:]
	i = strings.Index(name, ".")
	return name[i+1:]
}

// stackError wraps an error with a stack trace
type stackError struct {
	error
	*stack
	once sync.Once // Ensure stack is captured only once
}

// Unwrap returns the underlying error
func (w *stackError) Unwrap() error { return w.error }

// Format formats the error according to fmt.Formatter interface
func (w *stackError) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v':
		if s.Flag('+') {
			fmt.Fprintf(s, "%+v", w.Unwrap())
			w.stack.Format(s, verb)
			return
		}
		fallthrough
	case 's':
		io.WriteString(s, w.Error())
	case 'q':
		fmt.Fprintf(s, "%q", w.Error())
	}
}

// WithStack annotates err with a stack trace
func WithStack(err error) error {
	if err == nil {
		return nil
	}

	// If error already has a stack, don't add another
	if HasStack(err) {
		return err
	}

	return &stackError{
		error: err,
		stack: callers(),
	}
}

// GetStack returns the stacktrace of the first error with a stack
func GetStack(err error) (StackTrace, bool) {
	var target *stackError
	ok := errors.As(err, &target)
	if !ok {
		return nil, false
	}
	return target.StackTrace(), true
}

// HasStack returns true if any error in err's tree has a stacktrace
func HasStack(err error) bool {
	var target *stackError
	return errors.As(err, &target)
}
