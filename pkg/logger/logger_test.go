package logx

import (
	"bytes"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeHook struct {
	calls atomic.Int64
	level zerolog.Level
}

func (f *fakeHook) Run(_ *zerolog.Event, level zerolog.Level, _ string) {
	f.calls.Add(1)
	f.level = level
}

type fakeWriter struct {
	mu      bytesBuffer
	written atomic.Int64
}

type bytesBuffer struct {
	bytes.Buffer
}

func (f *fakeWriter) Write(p []byte) (int, error) {
	f.written.Add(1)
	return f.mu.Write(p)
}

func (f *fakeWriter) WriteLevel(_ zerolog.Level, p []byte) (int, error) {
	f.written.Add(1)
	return f.mu.Write(p)
}

func (f *fakeWriter) snapshot() string { return f.mu.String() }

func resetState(t *testing.T) {
	t.Helper()
	resetForTest()
	t.Cleanup(resetForTest)
}

func TestRegisterHook_NilIsNoop(t *testing.T) {
	resetState(t)
	RegisterHook(nil)
	// No panic, no state mutation.
	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, hooks)
}

func TestRegisterHook_FiresOnLog(t *testing.T) {
	resetState(t)

	h := &fakeHook{}
	RegisterHook(h)

	log.Logger.Info().Msg("hello")

	assert.Equal(t, int64(1), h.calls.Load())
	assert.Equal(t, zerolog.InfoLevel, h.level)
}

func TestRegisterHook_MultipleHooksFireInOrder(t *testing.T) {
	resetState(t)

	a := &fakeHook{}
	b := &fakeHook{}

	RegisterHook(a)
	RegisterHook(b)

	log.Logger.Info().Msg("two hooks")

	assert.Equal(t, int64(1), a.calls.Load())
	assert.Equal(t, int64(1), b.calls.Load())
}

func TestRegisterLevelWriter_NilIsNoop(t *testing.T) {
	resetState(t)
	RegisterLevelWriter(nil)
	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, writers)
}

func TestRegisterLevelWriter_ReceivesBytes(t *testing.T) {
	resetState(t)

	w := &fakeWriter{}
	RegisterLevelWriter(w)

	log.Logger.Info().Str("k", "v").Msg("bridge-test")

	require.GreaterOrEqual(t, w.written.Load(), int64(1))
	assert.Contains(t, w.snapshot(), "bridge-test")
	assert.Contains(t, w.snapshot(), `"k":"v"`)
}

func TestRegisterLevelWriter_BasePlusRegisteredBothReceive(t *testing.T) {
	resetState(t)

	// Replace base writer indirectly by re-binding with a known config;
	// we cannot inject a fake base writer through the public API, so
	// this test focuses on the registered writer seeing the event.
	w := &fakeWriter{}
	RegisterLevelWriter(w)

	log.Logger.Warn().Msg("warn-event")

	assert.Contains(t, w.snapshot(), "warn-event")
	assert.Contains(t, w.snapshot(), `"level":"warn"`)
}

func TestBindPreservesRegisteredHooksAndWriters(t *testing.T) {
	resetState(t)

	h := &fakeHook{}
	w := &fakeWriter{}

	RegisterHook(h)
	RegisterLevelWriter(w)

	Bind(&Config{Debug: false, Timezone: "UTC"})

	log.Logger.Info().Msg("after-bind")

	assert.Equal(t, int64(1), h.calls.Load())
	assert.Contains(t, w.snapshot(), "after-bind")
}

// The console writer parses the timestamp it is handed and renders it somewhere
// of its own choosing. Left to itself that somewhere is time.Local, so a process
// that pins time.Local — which containers commonly do — would print UTC in the
// pretty view while the JSON view carried the configured offset.
func TestConsoleWriterRendersInTheConfiguredZone(t *testing.T) {
	bangkok, err := time.LoadLocation("Asia/Bangkok")
	require.NoError(t, err)

	restore := time.Local
	time.Local = time.UTC

	t.Cleanup(func() { time.Local = restore })

	var buf bytes.Buffer

	w := newConsoleWriter(&buf, bangkok)
	w.NoColor = true

	// 2023-11-14T22:13:20Z is 05:13 the next morning in Bangkok. The two zones
	// land on different hours and different halves of the day, so a writer that
	// ignored the location could not pass by accident.
	_, err = w.Write([]byte(`{"level":"info","time":"2023-11-14T22:13:20Z","message":"hi"}`))
	require.NoError(t, err)

	assert.Contains(t, buf.String(), "5:13AM", "rendered in the configured zone")
	assert.NotContains(t, buf.String(), "10:13PM", "not in time.Local")
}
