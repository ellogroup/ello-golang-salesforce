package salesforce

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func Test_newZapLogger(t *testing.T) {
	tests := []struct {
		name      string
		handlerLv slog.Level
		log       func(l *zap.Logger)
		want      []map[string]any
	}{
		{
			name:      "Info with fields, forwards message, level and fields",
			handlerLv: slog.LevelInfo,
			log: func(l *zap.Logger) {
				l.Info("hello", zap.String("s", "v"), zap.Int("i", 3), zap.Error(errors.New("boom")))
			},
			want: []map[string]any{{"level": "INFO", "msg": "hello", "s": "v", "i": float64(3), "error": "boom"}},
		},
		{
			name:      "Named logger and With fields, adds logger name and fields",
			handlerLv: slog.LevelInfo,
			log: func(l *zap.Logger) {
				l.Named("Cache").With(zap.String("w", "x")).Warn("warned")
			},
			want: []map[string]any{{"level": "WARN", "msg": "warned", "logger": "Cache", "w": "x"}},
		},
		{
			name:      "Error, maps to slog error level",
			handlerLv: slog.LevelInfo,
			log: func(l *zap.Logger) {
				l.Error("failed")
			},
			want: []map[string]any{{"level": "ERROR", "msg": "failed"}},
		},
		{
			name:      "Debug below handler level, is dropped",
			handlerLv: slog.LevelInfo,
			log: func(l *zap.Logger) {
				l.Debug("dropped")
			},
			want: nil,
		},
		{
			name:      "Debug at handler level, is forwarded",
			handlerLv: slog.LevelDebug,
			log: func(l *zap.Logger) {
				l.Debug("kept")
			},
			want: []map[string]any{{"level": "DEBUG", "msg": "kept"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			h := slog.NewJSONHandler(buf, &slog.HandlerOptions{
				Level: tt.handlerLv,
				ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
					if a.Key == slog.TimeKey {
						return slog.Attr{}
					}
					return a
				},
			})

			tt.log(newZapLogger(h))

			var got []map[string]any
			dec := json.NewDecoder(buf)
			for dec.More() {
				var line map[string]any
				require.NoError(t, dec.Decode(&line))
				got = append(got, line)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
