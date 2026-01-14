package exec

import (
	"context"
	"sync"

	"github.com/vcnkl/rpm/dag"
)

type ParallelExecutor struct {
	maxWorkers int
}

func NewParallelExecutor(maxWorkers int) *ParallelExecutor {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	return &ParallelExecutor{maxWorkers: maxWorkers}
}

type TaskFunc func(ctx context.Context, node *dag.Node) error

func (p *ParallelExecutor) Execute(ctx context.Context, nodes []*dag.Node, fn TaskFunc) map[string]error {
	results := make(map[string]error)
	var mu sync.Mutex

	completed := make(map[string]bool)
	var completedMu sync.Mutex

	sem := make(chan struct{}, p.maxWorkers)
	var wg sync.WaitGroup

	for _, node := range nodes {
		wg.Add(1)
		go func(n *dag.Node) {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					mu.Lock()
					results[n.ID] = ctx.Err()
					mu.Unlock()
					return
				default:
				}

				allDepsDone := true
				anyDepFailed := false

				completedMu.Lock()
				for _, dep := range n.Deps {
					if !completed[dep.ID] {
						allDepsDone = false
						break
					}
					mu.Lock()
					if results[dep.ID] != nil {
						anyDepFailed = true
					}
					mu.Unlock()
				}
				completedMu.Unlock()

				if anyDepFailed {
					mu.Lock()
					results[n.ID] = &DependencyFailedError{TargetID: n.ID}
					mu.Unlock()
					completedMu.Lock()
					completed[n.ID] = true
					completedMu.Unlock()
					return
				}

				if allDepsDone {
					break
				}
			}

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				results[n.ID] = ctx.Err()
				mu.Unlock()
				return
			}

			err := fn(ctx, n)

			<-sem

			mu.Lock()
			results[n.ID] = err
			mu.Unlock()

			completedMu.Lock()
			completed[n.ID] = true
			completedMu.Unlock()
		}(node)
	}

	wg.Wait()

	return results
}

type DependencyFailedError struct {
	TargetID string
}

func (e *DependencyFailedError) Error() string {
	return "dependency failed for target: " + e.TargetID
}
