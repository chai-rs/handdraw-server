// Package logx configures structured application logging at startup.
package logx

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	errxzerolog "github.com/chai-rs/handdraw-server/pkg/error/logger"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func init() { Bind(nil) }

// Config controls the behaviour of the package-global logger.
type Config struct {
	Debug        bool   `split_words:"true" default:"false"`
	PrettyFormat bool   `split_words:"true" default:"false"`
	Timezone     string `split_words:"true" default:"UTC"`
}

var (
	mu         sync.Mutex
	currentCfg Config
	hooks      []zerolog.Hook
	writers    []zerolog.LevelWriter
)

// Bind installs the errx marshalling hooks and rebuilds the package-global
// logger from conf. A nil conf applies the zero-value defaults. Hooks and level
// writers registered earlier are preserved.
func Bind(conf *Config) {
	zerolog.ErrorStackMarshaler = errxzerolog.StackMarshaller
	zerolog.ErrorMarshalFunc = errxzerolog.MarshalFunc
	zerolog.CallerMarshalFunc = func(pc uintptr, file string, line int) string {
		// shorten: keep last 2 path segments (e.g. "engine/listener.go:52")
		parts := strings.Split(file, "/")
		if len(parts) > 3 {
			file = strings.Join(parts[len(parts)-3:], "/")
		}

		fn := runtime.FuncForPC(pc)
		if fn != nil {
			name := fn.Name()
			if i := strings.LastIndex(name, "/"); i >= 0 {
				name = name[i+1:]
			}

			return fmt.Sprintf("%s:%d (%s)", file, line, name)
		}

		return fmt.Sprintf("%s:%d", file, line)
	}

	if conf == nil {
		conf = &Config{
			Debug:        false,
			PrettyFormat: false,
			Timezone:     "UTC",
		}
	}

	mu.Lock()
	defer mu.Unlock()

	currentCfg = *conf

	rebuildLocked()
}

// RegisterHook installs a zerolog.Hook on the package-global logger.
// Hooks fire in registration order before the event is serialized.
// Safe to call concurrently; pkg/otel attaches its trace-correlation
// hook via this entry point so pkg/logger never imports pkg/otel.
func RegisterHook(h zerolog.Hook) {
	if h == nil {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	hooks = append(hooks, h)

	rebuildLocked()
}

// RegisterLevelWriter installs an additional zerolog.LevelWriter that
// receives every serialized log event in parallel with the base writer.
// Safe to call concurrently; pkg/otel attaches its OTLP log bridge here.
func RegisterLevelWriter(w zerolog.LevelWriter) {
	if w == nil {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	writers = append(writers, w)

	rebuildLocked()
}

// newConsoleWriter builds the pretty writer, rendering timestamps in loc.
//
// Without TimeLocation the console writer re-parses the timestamp it was handed
// and renders it in time.Local, which is the host's zone rather than the
// configured one. The two outputs would then disagree — JSON carrying the
// configured offset while the pretty view showed the host's — and a process
// that pins time.Local, as containers commonly do, would print UTC here while
// claiming UTC everywhere else.
//
// It takes its output rather than reaching for os.Stdout so that the rendering
// can be asserted.
func newConsoleWriter(out io.Writer, loc *time.Location) zerolog.ConsoleWriter {
	return zerolog.NewConsoleWriter(func(w *zerolog.ConsoleWriter) {
		w.Out = out
		w.TimeLocation = loc
	})
}

func rebuildLocked() {
	tzName := currentCfg.Timezone
	if tzName == "" {
		tzName = "UTC"
	}

	loc, err := time.LoadLocation(tzName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger: invalid timezone %q, falling back to UTC: %v\n", tzName, err)

		loc = time.UTC
	}

	zerolog.TimestampFunc = func() time.Time { return time.Now().In(loc) }

	level := zerolog.InfoLevel
	if currentCfg.Debug {
		level = zerolog.DebugLevel
	}

	var base io.Writer = os.Stdout
	if currentCfg.PrettyFormat {
		base = newConsoleWriter(os.Stdout, loc)
	}

	out := base

	if len(writers) > 0 {
		all := make([]io.Writer, 0, len(writers)+1)
		all = append(all, base)

		for _, w := range writers {
			all = append(all, w)
		}

		out = zerolog.MultiLevelWriter(all...)
	}

	logger := zerolog.New(out).Level(level).With().
		Timestamp().
		Stack().
		Logger()
	for _, h := range hooks {
		logger = logger.Hook(h)
	}

	log.Logger = logger
}

// resetForTest clears registered hooks and writers and rebuilds the
// logger from the current config. Test-only.
func resetForTest() {
	mu.Lock()
	defer mu.Unlock()

	hooks = nil
	writers = nil

	rebuildLocked()
}

// Debug starts a debug-level event on the package-global logger, attributing
// the caller of this function as the log site.
func Debug() *zerolog.Event {
	return log.Debug().Caller(1)
}

// Info starts an info-level event on the package-global logger, attributing the
// caller of this function as the log site.
func Info() *zerolog.Event {
	return log.Info().Caller(1)
}

// Warn starts a warn-level event on the package-global logger, attributing the
// caller of this function as the log site.
func Warn() *zerolog.Event {
	return log.Warn().Caller(1)
}

// Error starts an error-level event on the package-global logger, attributing
// the caller of this function as the log site.
func Error() *zerolog.Event {
	return log.Error().Caller(1)
}

// Panic starts a panic-level event on the package-global logger, attributing
// the caller of this function as the log site. Writing the event panics.
func Panic() *zerolog.Event {
	return log.Panic().Caller(1)
}

// Fatal starts a fatal-level event on the package-global logger, attributing
// the caller of this function as the log site. Writing the event exits the
// process.
func Fatal() *zerolog.Event {
	return log.Fatal().Caller(1)
}
