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
	Output(out io.Writer) io.Writer
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
	zlog        zerolog.Logger
	prefix      string
	destination bool
}

func New(level Level) Logger {
	return NewWithDateTimeFormat(level, "")
}

func NewWithDateTimeFormat(level Level, dateTimeFormat string, destinations ...io.Writer) Logger {
	dateTimeFormat = normalizeDateTimeFormat(dateTimeFormat)
	zerolog.TimeFieldFormat = dateTimeFormat

	out := os.Stdout
	var terminal io.Writer = out

	if isatty.IsTerminal(out.Fd()) {
		terminal = zerolog.ConsoleWriter{
			Out:        out,
			TimeFormat: dateTimeFormat,
		}
	}
	writers := []io.Writer{terminal}
	hasDestination := false
	for _, destination := range destinations {
		if destination != nil {
			writers = append(writers, destination)
			hasDestination = true
		}
	}
	zl := zerolog.New(zerolog.MultiLevelWriter(writers...)).With().Timestamp().Logger()

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

	return &logger{zlog: zl, destination: hasDestination}
}

func normalizeDateTimeFormat(dateTimeFormat string) string {
	if strings.TrimSpace(dateTimeFormat) == "" {
		return time.RFC3339
	}
	return dateTimeFormat
}

func (l *logger) WithPrefix(prefix string) Logger {
	return &logger{
		zlog:        l.zlog.With().Str("target", prefix).Logger(),
		prefix:      prefix,
		destination: l.destination,
	}
}

func (l *logger) Writer() io.Writer {
	return &writer{logger: l}
}

func (l *logger) Output(out io.Writer) io.Writer {
	if !l.destination {
		return out
	}
	return l.Writer()
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
