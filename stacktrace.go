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

// stackPool holds reusable slices of uintptr for stack trace capture.
// This sync.Pool helps reduce memory allocations and GC pressure
// when capturing stack traces frequently.
var stackPool = sync.Pool{
	New: func() interface{} {
		// Pre-allocate a slice with a reasonable default capacity (32 frames).
		s := make([]uintptr, 32)
		return &s
	},
}

// Frame represents a single program counter inside a stack frame.
type Frame uintptr

// pc returns the program counter for this frame.
// It subtracts 1 because runtime.Callers and runtime.FuncForPC
// use different conventions for the program counter.
func (f Frame) pc() uintptr { return uintptr(f) - 1 }

// file returns the full path to the file for this Frame's pc.
func (f Frame) file() string {
	// Find the function associated with this program counter.
	fn := runtime.FuncForPC(f.pc())
	if fn == nil {
		return "unknown"
	}
	// Get the file and line number from the function.
	file, _ := fn.FileLine(f.pc())
	return file
}

// line returns the line number of source code for this Frame's pc.
func (f Frame) line() int {
	fn := runtime.FuncForPC(f.pc())
	if fn == nil {
		return 0
	}
	_, line := fn.FileLine(f.pc())
	return line
}

// name returns the name of this function.
func (f Frame) name() string {
	fn := runtime.FuncForPC(f.pc())
	if fn == nil {
		return "unknown"
	}
	return fn.Name()
}

// Format implements the fmt.Formatter interface for formatting a Frame.
// This allows custom formatting verbs like %+v.
//
// Verbs:
//
//	%s  - prints the file name (e.g., "stacktrace.go")
//	%+s - prints the full function name and file path (e.g., "main.go\n\t/path/to/main.go")
//	%d  - prints the line number
//	%n  - prints the function name (e.g., "main")
//	%v  - prints file:line (e.g., "stacktrace.go:100")
//	%+v - prints function name, file, and line (e.g., "main.go\n\t/path/to/main.go:100")
func (f Frame) Format(s fmt.State, verb rune) {
	var err error
	switch verb {
	case 's':
		switch {
		case s.Flag('+'):
			// Verbose: print function name and full file path.
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
			// Default: print base file name.
			_, err = io.WriteString(s, path.Base(f.file()))
			if err != nil {
				log.Printf("Error writing file base name: %v", err) // Log the error
				return
			}
		}
	case 'd':
		// Print line number.
		_, err = io.WriteString(s, strconv.Itoa(f.line()))
		if err != nil {
			log.Printf("Error writing line number: %v", err) // Log the error
			return
		}
	case 'n':
		// Print simple function name.
		_, err = io.WriteString(s, funcname(f.name()))
		if err != nil {
			log.Printf("Error writing function name: %v", err) // Log the error
			return
		}
	case 'v':
		// Default verb: format as "file:line"
		f.Format(s, 's') // 's' gives file name
		_, err = io.WriteString(s, ":")
		if err != nil {
			log.Printf("Error writing colon: %v", err) // Log the error
			return
		}
		f.Format(s, 'd') // 'd' gives line number
	}
}

// MarshalText implements the encoding.TextMarshaler interface.
// This formats a stack frame as text, useful for logging or JSON encoding.
func (f Frame) MarshalText() ([]byte, error) {
	name := f.name()
	if name == "unknown" {
		return []byte(name), nil
	}
	// Format as "functionName /path/to/file:line"
	return []byte(fmt.Sprintf("%s %s:%d", name, f.file(), f.line())), nil
}

// StackTrace is a stack of Frames from innermost (most recent) to outermost.
type StackTrace []Frame

// Format implements the fmt.Formatter interface for formatting a StackTrace.
//
// Verbs:
//
//	%v  - prints a collapsed slice of frames
//	%+v - prints a full, multi-line stack trace
func (st StackTrace) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v':
		switch {
		case s.Flag('+'):
			// Verbose: print each frame on a new line with its details.
			for _, f := range st {
				io.WriteString(s, "\n")
				f.Format(s, verb)
			}
		case s.Flag('#'):
			// Go-syntax representation.
			fmt.Fprintf(s, "%#v", []Frame(st))
		default:
			// Default: format as a single-line slice.
			st.formatSlice(s, verb)
		}
	case 's':
		// Format as a single-line slice.
		st.formatSlice(s, verb)
	}
}

// formatSlice is a helper to format the stack trace as a slice of Frames.
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

// stack represents a raw stack of program counters.
type stack []uintptr

// Format implements the fmt.Formatter interface for a raw stack.
// It's primarily used to print the full trace when formatting a stackError.
func (s *stack) Format(st fmt.State, verb rune) {
	switch verb {
	case 'v':
		switch {
		case st.Flag('+'):
			// Verbose: format each PC as a full Frame.
			for _, pc := range *s {
				f := Frame(pc)
				fmt.Fprintf(st, "\n%+v", f)
			}
		}
	}
}

// StackTrace converts the raw stack of PCs into a user-facing StackTrace (slice of Frames).
func (s *stack) StackTrace() StackTrace {
	f := make([]Frame, len(*s))
	for i := 0; i < len(f); i++ {
		f[i] = Frame((*s)[i])
	}
	return f
}

// callersMu protects the stackPool and runtime.Callers from concurrent access.
var callersMu sync.Mutex

// callers captures the current goroutine's stack trace.
// It uses the stackPool to avoid allocating a new slice for program counters each time.
func callers() *stack {
	callersMu.Lock()
	defer callersMu.Unlock()

	// Get a slice from the pool.
	pcsPtr := stackPool.Get().(*[]uintptr)
	pcs := *pcsPtr

	// Capture the stack trace.
	// Skip 3 frames: runtime.Callers, callers(), WithStack()
	n := runtime.Callers(3, pcs)

	// Create a new stack of the exact size needed.
	result := make(stack, n)
	copy(result, pcs[0:n])

	// Return the slice to the pool for reuse.
	stackPool.Put(pcsPtr)

	return &result
}

// funcname removes the path prefix component of a function's name.
// e.g., "github.com/my/package.MyFunction" becomes "MyFunction".
func funcname(name string) string {
	i := strings.LastIndex(name, "/")
	name = name[i+1:]
	i = strings.Index(name, ".")
	return name[i+1:]
}

// stackError wraps an error with a stack trace.
type stackError struct {
	error            // The original error.
	*stack           // The stack trace captured when the error was wrapped.
	once   sync.Once // Ensures stack is captured only once (not currently used, but good practice).
}

// Unwrap returns the underlying error, implementing the errors.Wrapper interface.
// This allows errors.Is and errors.As to work.
func (w *stackError) Unwrap() error { return w.error }

// Format implements the fmt.Formatter interface for stackError.
func (w *stackError) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v':
		if s.Flag('+') {
			// Verbose: print the underlying error's verbose format,
			// then print our own stack trace.
			fmt.Fprintf(s, "%+v", w.Unwrap())
			w.stack.Format(s, verb)
			return
		}
		fallthrough
	case 's':
		// Default: print the underlying error's message.
		io.WriteString(s, w.Error())
	case 'q':
		// Quoted: print the underlying error's message, quoted.
		fmt.Fprintf(s, "%q", w.Error())
	}
}

// WithStack annotates err with a stack trace at the point WithStack was called.
// If err is nil, it returns nil.
// If err already has a stack trace, it returns err unmodified.
func WithStack(err error) error {
	if err == nil {
		return nil
	}

	// If error already has a stack, don't add another.
	if HasStack(err) {
		return err
	}

	// Wrap the error with a new stackError.
	return &stackError{
		error: err,
		stack: callers(), // Capture the stack trace now.
	}
}

// GetStack finds the first error in the chain that has a stack trace
// and returns the StackTrace if found.
func GetStack(err error) (StackTrace, bool) {
	var target *stackError
	// Use errors.As to search the error chain for a stackError.
	ok := errors.As(err, &target)
	if !ok {
		return nil, false
	}
	return target.StackTrace(), true
}

// HasStack returns true if any error in err's tree has a stacktrace.
func HasStack(err error) bool {
	var target *stackError
	// errors.As checks if err or any of its wrapped errors are of type *stackError.
	return errors.As(err, &target)
}
