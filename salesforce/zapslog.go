package salesforce

import (
	"context"
	"log/slog"
	"slices"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// newZapLogger returns a zap.Logger whose entries are forwarded to h, for passing a slog logger to
// ello-golang-cache v1, which only accepts a *zap.Logger. zap does not carry a context, so entries logged
// through it do not include context attributes.
func newZapLogger(h slog.Handler) *zap.Logger {
	return zap.New(&zapSlogCore{handler: h})
}

// zapSlogCore is a zapcore.Core that forwards entries to a slog.Handler.
type zapSlogCore struct {
	handler slog.Handler
}

// Enabled reports whether the wrapped handler is enabled for the equivalent slog level.
func (c *zapSlogCore) Enabled(lvl zapcore.Level) bool {
	return c.handler.Enabled(context.Background(), zapToSlogLevel(lvl))
}

// With returns a core whose handler has fields added as attributes.
func (c *zapSlogCore) With(fields []zapcore.Field) zapcore.Core {
	return &zapSlogCore{handler: c.handler.WithAttrs(zapFieldsToAttrs(fields))}
}

// Check adds this core to ce if the entry's level is enabled.
func (c *zapSlogCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

// Write converts the entry and its fields into a slog.Record and passes it to the handler.
func (c *zapSlogCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	r := slog.NewRecord(ent.Time, zapToSlogLevel(ent.Level), ent.Message, 0)
	if ent.LoggerName != "" {
		r.AddAttrs(slog.String("logger", ent.LoggerName))
	}
	r.AddAttrs(zapFieldsToAttrs(fields)...)
	return c.handler.Handle(context.Background(), r)
}

// Sync is a no-op, the slog handler writes synchronously.
func (*zapSlogCore) Sync() error {
	return nil
}

func zapToSlogLevel(lvl zapcore.Level) slog.Level {
	switch {
	case lvl <= zapcore.DebugLevel:
		return slog.LevelDebug
	case lvl == zapcore.InfoLevel:
		return slog.LevelInfo
	case lvl == zapcore.WarnLevel:
		return slog.LevelWarn
	default:
		return slog.LevelError
	}
}

func zapFieldsToAttrs(fields []zapcore.Field) []slog.Attr {
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range fields {
		f.AddTo(enc)
	}
	keys := make([]string, 0, len(enc.Fields))
	for k := range enc.Fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	attrs := make([]slog.Attr, 0, len(keys))
	for _, k := range keys {
		attrs = append(attrs, slog.Any(k, enc.Fields[k]))
	}
	return attrs
}
