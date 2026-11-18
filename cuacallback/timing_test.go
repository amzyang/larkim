package cuacallback

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTimedTransport_LogsDurationAndErrorWithoutChangingResult(t *testing.T) {
	t.Parallel()
	for _, fail := range []bool{false, true} {
		var log bytes.Buffer
		clock := time.Unix(0, 0)
		f := &fakeTransport{responses: []string{`{"activated":true}`}}
		if fail {
			f.responses = nil
		}
		c := TimedTransport{Transport: f, Log: slog.New(slog.NewJSONHandler(&log, nil)), Now: func() time.Time { v := clock; clock = clock.Add(37 * time.Millisecond); return v }}
		var out map[string]any
		err := c.Call(context.Background(), "bring_to_front", map[string]any{}, &out)
		if fail {
			require.Error(t, err)
			require.Contains(t, log.String(), "unexpected call")
		} else {
			require.NoError(t, err)
			require.Equal(t, true, out["activated"])
		}
		require.Contains(t, log.String(), `"dur_ms":37`)
		require.Contains(t, log.String(), `"tool":"bring_to_front"`)
	}
}
