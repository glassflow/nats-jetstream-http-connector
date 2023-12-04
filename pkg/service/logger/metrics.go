package logger

import (
	"context"
	"log/slog"
)

func SlogMetrics(h slog.Handler, counter func(level string)) slog.Handler {
	return slogMetrics{Handler: h, Counter: counter}
}

type slogMetrics struct {
	Handler slog.Handler
	Counter func(level string)
}

func (s slogMetrics) Handle(ctx context.Context, r slog.Record) error {
	switch r.Level {
	case slog.LevelError:
		s.Counter("error")
	case slog.LevelWarn:
		s.Counter("warn")
	case slog.LevelDebug, slog.LevelInfo:
		// ignore
	}
	return s.Handler.Handle(ctx, r) //nolint:wrapcheck // don't wrap on simple wrapper type
}

func (s slogMetrics) Enabled(ctx context.Context, l slog.Level) bool {
	return s.Handler.Enabled(ctx, l)
}

func (s slogMetrics) WithAttrs(attrs []slog.Attr) slog.Handler {
	return slogMetrics{s.Handler.WithAttrs(attrs), s.Counter}
}

func (s slogMetrics) WithGroup(name string) slog.Handler {
	return slogMetrics{s.Handler.WithGroup(name), s.Counter}
}
