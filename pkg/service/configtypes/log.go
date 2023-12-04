package configtypes

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

type LogLevel slog.Level

func (l *LogLevel) SetString(s string) error {
	var level slog.Level
	err := level.UnmarshalText([]byte(s))
	if err != nil {
		return fmt.Errorf("unmarshal slog.Level: %w", err)
	}
	*l = LogLevel(level)
	return nil
}

func (l LogLevel) Level() slog.Level { return slog.Level(l) }

type LogHandler func(io.Writer, *slog.HandlerOptions) slog.Handler

func (l *LogHandler) SetString(s string) error {
	switch strings.ToLower(s) {
	case "text":
		*l = func(w io.Writer, opts *slog.HandlerOptions) slog.Handler { return slog.NewTextHandler(w, opts) }
	case "json":
		*l = func(w io.Writer, opts *slog.HandlerOptions) slog.Handler { return slog.NewJSONHandler(w, opts) }
	default:
		return fmt.Errorf("wrong format: only 'text|json' are accepted")
	}
	return nil
}

func (l LogHandler) Handler(w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	if l == nil {
		return nil
	}
	return l(w, opts)
}

type AddSource bool

func (a AddSource) IsAddSource() bool { return bool(a) }
