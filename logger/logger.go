package logger

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog"
)

type Level int

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	WithPrefix(prefix string) Logger
	Writer() io.Writer
}

type Field struct {
	Key   string
	Value any
}

func String(key, value string) Field {
	return Field{Key: key, Value: value}
}

func Int(key string, value int) Field {
	return Field{Key: key, Value: value}
}

func Err(err error) Field {
	return Field{Key: "error", Value: err}
}

func Duration(key string, value time.Duration) Field {
	return Field{Key: key, Value: value}
}

func Bool(key string, value bool) Field {
	return Field{Key: key, Value: value}
}

type logger struct {
	zlog   zerolog.Logger
	prefix string
}

func New(level Level) Logger {
	out := os.Stdout
	var zl zerolog.Logger

	if isatty.IsTerminal(out.Fd()) {
		zl = zerolog.New(zerolog.ConsoleWriter{
			Out:        out,
			TimeFormat: time.RFC3339,
		}).With().Timestamp().Logger()
	} else {
		zl = zerolog.New(out).With().Timestamp().Logger()
	}

	switch level {
	case DebugLevel:
		zl = zl.Level(zerolog.DebugLevel)
	case InfoLevel:
		zl = zl.Level(zerolog.InfoLevel)
	case WarnLevel:
		zl = zl.Level(zerolog.WarnLevel)
	case ErrorLevel:
		zl = zl.Level(zerolog.ErrorLevel)
	}

	return &logger{zlog: zl}
}

func (l *logger) WithPrefix(prefix string) Logger {
	return &logger{
		zlog:   l.zlog.With().Str("target", prefix).Logger(),
		prefix: prefix,
	}
}

func (l *logger) Writer() io.Writer {
	return &writer{logger: l}
}

func (l *logger) applyFields(event *zerolog.Event, fields []Field) *zerolog.Event {
	for _, f := range fields {
		switch v := f.Value.(type) {
		case string:
			event = event.Str(f.Key, v)
		case int:
			event = event.Int(f.Key, v)
		case int64:
			event = event.Int64(f.Key, v)
		case bool:
			event = event.Bool(f.Key, v)
		case time.Duration:
			event = event.Dur(f.Key, v)
		case error:
			if v != nil {
				event = event.Err(v)
			}
		default:
			event = event.Interface(f.Key, v)
		}
	}
	return event
}

func (l *logger) Debug(msg string, fields ...Field) {
	l.applyFields(l.zlog.Debug(), fields).Msg(msg)
}

func (l *logger) Info(msg string, fields ...Field) {
	l.applyFields(l.zlog.Info(), fields).Msg(msg)
}

func (l *logger) Warn(msg string, fields ...Field) {
	l.applyFields(l.zlog.Warn(), fields).Msg(msg)
}

func (l *logger) Error(msg string, fields ...Field) {
	l.applyFields(l.zlog.Error(), fields).Msg(msg)
}

type writer struct {
	logger *logger
}

func (w *writer) Write(p []byte) (n int, err error) {
	w.logger.Info(strings.TrimSuffix(string(p), "\n"))
	return len(p), nil
}
