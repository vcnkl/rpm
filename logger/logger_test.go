package logger

import (
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

func TestFieldConstructors(t *testing.T) {
	tests := []struct {
		name     string
		field    Field
		expected Field
	}{
		{
			name:     "String field",
			field:    String("key", "value"),
			expected: Field{Key: "key", Value: "value"},
		},
		{
			name:     "Int field",
			field:    Int("count", 42),
			expected: Field{Key: "count", Value: 42},
		},
		{
			name:     "Bool field",
			field:    Bool("enabled", true),
			expected: Field{Key: "enabled", Value: true},
		},
		{
			name:     "Duration field",
			field:    Duration("elapsed", 5*time.Second),
			expected: Field{Key: "elapsed", Value: 5 * time.Second},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected.Key, tt.field.Key)
			assert.Equal(t, tt.expected.Value, tt.field.Value)
		})
	}
}

func TestErr(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected Field
	}{
		{
			name:     "non-nil error",
			err:      errors.New("test error"),
			expected: Field{Key: "error"},
		},
		{
			name:     "nil error",
			err:      nil,
			expected: Field{Key: "error", Value: nil},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field := Err(tt.err)
			assert.Equal(t, "error", field.Key)
			if tt.err != nil {
				assert.Error(t, field.Value.(error))
			} else {
				assert.Nil(t, field.Value)
			}
		})
	}
}

func TestLevel(t *testing.T) {
	tests := []struct {
		name     string
		level    Level
		expected int
	}{
		{
			name:     "debug level",
			level:    DebugLevel,
			expected: 0,
		},
		{
			name:     "info level",
			level:    InfoLevel,
			expected: 1,
		},
		{
			name:     "warn level",
			level:    WarnLevel,
			expected: 2,
		},
		{
			name:     "error level",
			level:    ErrorLevel,
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, int(tt.level))
		})
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name  string
		level Level
	}{
		{
			name:  "debug logger",
			level: DebugLevel,
		},
		{
			name:  "info logger",
			level: InfoLevel,
		},
		{
			name:  "warn logger",
			level: WarnLevel,
		},
		{
			name:  "error logger",
			level: ErrorLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := New(tt.level)
			assert.NotNil(t, log)
		})
	}
}

func TestLogger_WithPrefix(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
	}{
		{
			name:   "simple prefix",
			prefix: "myapp",
		},
		{
			name:   "target prefix",
			prefix: "core:app_build",
		},
		{
			name:   "empty prefix",
			prefix: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := New(InfoLevel)
			prefixed := log.WithPrefix(tt.prefix)
			assert.NotNil(t, prefixed)
		})
	}
}

func TestLogger_Writer(t *testing.T) {
	log := New(InfoLevel)
	writer := log.Writer()
	assert.NotNil(t, writer)

	n, err := writer.Write([]byte("test message"))
	assert.NoError(t, err)
	assert.Equal(t, 12, n)
}
