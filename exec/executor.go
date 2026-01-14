package exec

import (
	"context"
	"time"

	"github.com/vcnkl/rpm/logger"
	"github.com/vcnkl/rpm/models"
)

type Executor interface {
	Execute(ctx context.Context, opts *Options) (*models.Result, error)
}

type Options struct {
	Targets  []string
	Force    bool
	Parallel int
	Logger   logger.Logger
}

type TargetResult struct {
	TargetID string
	Success  bool
	Skipped  bool
	Error    error
	ExitCode int
	Duration time.Duration
}
